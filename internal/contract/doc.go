// Package contract defines the canonical document — the multi-source contract that
// every source normalizes to before Redpanda. Downstream code only ever sees this shape.
package contract

import (
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
)

// Op is the mutation kind carried by a canonical doc.
type Op string

const (
	OpUpsert Op = "upsert"
	OpDelete Op = "delete"
)

// Tier decides which Redpanda lane a doc travels on.
type Tier string

const (
	TierUrgent Tier = "urgent" // -> docs.fast
	TierBulk   Tier = "bulk"   // -> docs.bulk
)

// CanonicalDoc is the single shape emitted by every normalizer.
type CanonicalDoc struct {
	Source   string    `json:"source"`
	SourceID string    `json:"source_id"`
	DocID    string    `json:"doc_id"`
	TenantID int64     `json:"tenant_id"`
	Op       Op        `json:"op"`
	Tier     Tier      `json:"tier"`
	Version  int64     `json:"version"`
	CommitTS time.Time `json:"commit_ts"`
	Title    string    `json:"title,omitempty"`
	Body     string    `json:"body,omitempty"`
	ChunkID  int       `json:"chunk_id,omitempty"`
	Metadata Metadata  `json:"metadata,omitempty"`
}

// Metadata is free-form provenance carried through to the search stores.
type Metadata struct {
	Category string `json:"category,omitempty"`
	URL      string `json:"url,omitempty"`
	Author   string `json:"author,omitempty"`
}

// NamespacedDocID builds a globally unique, source-namespaced id.
// Format: {source}:{native_id}. Chunked docs append :{chunk}.
func NamespacedDocID(source, nativeID string) string {
	return fmt.Sprintf("%s:%s", source, nativeID)
}

// Validate enforces the required-field contract. Invalid docs are DLQ candidates.
func (d *CanonicalDoc) Validate() error {
	if d.Source == "" {
		return errors.New("canonical doc: source is required")
	}
	if d.DocID == "" {
		return errors.New("canonical doc: doc_id is required")
	}
	if d.Op != OpUpsert && d.Op != OpDelete {
		return fmt.Errorf("canonical doc: invalid op %q", d.Op)
	}
	if d.Tier != TierUrgent && d.Tier != TierBulk {
		return fmt.Errorf("canonical doc: invalid tier %q", d.Tier)
	}
	if d.Version < 0 {
		return fmt.Errorf("canonical doc: version must be >= 0, got %d", d.Version)
	}
	if d.CommitTS.IsZero() {
		return errors.New("canonical doc: commit_ts is required")
	}
	if d.Op == OpUpsert && d.Body == "" && d.Title == "" {
		return errors.New("canonical doc: upsert requires title or body")
	}
	return nil
}

// PointID derives a deterministic UUIDv5 (RFC 4122, SHA-1) for Qdrant from the
// doc_id, so re-processing the same doc overwrites the same point (idempotent
// upsert). The namespace is the standard DNS namespace; the value is stable.
func (d *CanonicalDoc) PointID() string {
	return uuid.NewSHA1(uuid.NameSpaceDNS, []byte(d.DocID)).String()
}

// Text returns the concatenated searchable text used for embedding.
func (d *CanonicalDoc) Text() string {
	if d.Title == "" {
		return d.Body
	}
	if d.Body == "" {
		return d.Title
	}
	return d.Title + "\n\n" + d.Body
}
