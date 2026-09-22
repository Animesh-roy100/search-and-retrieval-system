// Package normalizer converts source-specific change events into the canonical doc
// shape and classifies each into a priority tier. This is the ONLY source-aware code;
// everything downstream is source-agnostic.
package normalizer

import (
	"encoding/json"
	"fmt"
	"time"

	"github.com/animeshroy/search-and-retrieval-system/internal/contract"
)

const SourcePostgres = "postgres"

// debeziumEnvelope is the unwrapped Debezium JSON value (schemas disabled).
type debeziumEnvelope struct {
	Before *documentRow `json:"before"`
	After  *documentRow `json:"after"`
	Op     string       `json:"op"` // c=create, u=update, d=delete, r=read(snapshot)
	Source struct {
		TsMs  int64  `json:"ts_ms"`
		LSN   int64  `json:"lsn"`
		Table string `json:"table"`
	} `json:"source"`
	TsMs int64 `json:"ts_ms"`
}

// documentRow mirrors the public.documents table.
type documentRow struct {
	ID        int64  `json:"id"`
	TenantID  int64  `json:"tenant_id"`
	Title     string `json:"title"`
	Body      string `json:"body"`
	Category  string `json:"category"`
	Tier      string `json:"tier"`
	UpdatedAt string `json:"updated_at"` // ISO-8601 (time.precision.mode=connect); not used for logic
	Version   int64  `json:"version"`
}

// ErrSkip signals a change event that should be acknowledged but not indexed
// (e.g. a Debezium heartbeat or truncate).
var ErrSkip = fmt.Errorf("normalizer: skip event")

// FromDebezium parses one Debezium value payload into a canonical doc.
func FromDebezium(value []byte) (contract.CanonicalDoc, error) {
	var env debeziumEnvelope
	if err := json.Unmarshal(value, &env); err != nil {
		return contract.CanonicalDoc{}, fmt.Errorf("normalizer: decode envelope: %w", err)
	}

	switch env.Op {
	case "c", "u", "r": // create / update / snapshot-read -> upsert
		if env.After == nil {
			return contract.CanonicalDoc{}, fmt.Errorf("normalizer: op %q with nil after", env.Op)
		}
		return buildDoc(env.After, contract.OpUpsert, env.commitTS()), nil
	case "d": // delete
		row := env.Before
		if row == nil {
			return contract.CanonicalDoc{}, fmt.Errorf("normalizer: delete with nil before")
		}
		return buildDoc(row, contract.OpDelete, env.commitTS()), nil
	case "t", "": // truncate / heartbeat / empty
		return contract.CanonicalDoc{}, ErrSkip
	default:
		return contract.CanonicalDoc{}, fmt.Errorf("normalizer: unknown op %q", env.Op)
	}
}

func (e debeziumEnvelope) commitTS() time.Time {
	ms := e.Source.TsMs
	if ms == 0 {
		ms = e.TsMs
	}
	if ms == 0 {
		return time.Now().UTC()
	}
	return time.UnixMilli(ms).UTC()
}

func buildDoc(row *documentRow, op contract.Op, commit time.Time) contract.CanonicalDoc {
	nativeID := fmt.Sprintf("documents:%d", row.ID)
	return contract.CanonicalDoc{
		Source:   SourcePostgres,
		SourceID: nativeID,
		DocID:    contract.NamespacedDocID(SourcePostgres, nativeID),
		TenantID: row.TenantID,
		Op:       op,
		Tier:     classifyTier(op, row),
		Version:  row.Version,
		CommitTS: commit,
		Title:    row.Title,
		Body:     row.Body,
		Metadata: contract.Metadata{Category: row.Category},
	}
}

// classifyTier is the per-source urgency rule. Deletes are always urgent (a stale
// visible doc is worse than a stale hidden one); otherwise honour the row's tier column.
func classifyTier(op contract.Op, row *documentRow) contract.Tier {
	if op == contract.OpDelete {
		return contract.TierUrgent
	}
	if row.Tier == string(contract.TierUrgent) {
		return contract.TierUrgent
	}
	return contract.TierBulk
}
