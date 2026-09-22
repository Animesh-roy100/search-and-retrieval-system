package normalizer

import (
	"testing"

	"github.com/animeshroy/search-and-retrieval-system/internal/contract"
)

func TestFromDebeziumCreate(t *testing.T) {
	raw := []byte(`{
		"op":"c",
		"after":{"id":42,"tenant_id":7,"title":"T","body":"B","category":"infra","tier":"urgent","version":1},
		"source":{"ts_ms":1700000000000,"lsn":123,"table":"documents"}
	}`)
	d, err := FromDebezium(raw)
	if err != nil {
		t.Fatal(err)
	}
	if d.Op != contract.OpUpsert {
		t.Fatalf("want upsert, got %s", d.Op)
	}
	if d.DocID != "postgres:documents:42" {
		t.Fatalf("bad doc_id: %s", d.DocID)
	}
	if d.Tier != contract.TierUrgent {
		t.Fatalf("want urgent tier, got %s", d.Tier)
	}
	if d.Version != 1 || d.TenantID != 7 {
		t.Fatalf("bad fields: %+v", d)
	}
	if d.CommitTS.IsZero() {
		t.Fatal("commit_ts not derived from source.ts_ms")
	}
	if err := d.Validate(); err != nil {
		t.Fatalf("produced doc should be valid: %v", err)
	}
}

func TestFromDebeziumUpdateBulkTier(t *testing.T) {
	raw := []byte(`{
		"op":"u",
		"before":{"id":42,"version":1,"tier":"bulk"},
		"after":{"id":42,"tenant_id":1,"title":"T","body":"B2","tier":"bulk","version":2},
		"source":{"ts_ms":1700000000000,"table":"documents"}
	}`)
	d, err := FromDebezium(raw)
	if err != nil {
		t.Fatal(err)
	}
	if d.Tier != contract.TierBulk {
		t.Fatalf("want bulk tier, got %s", d.Tier)
	}
	if d.Version != 2 {
		t.Fatalf("want version 2, got %d", d.Version)
	}
}

func TestFromDebeziumDeleteIsUrgent(t *testing.T) {
	raw := []byte(`{
		"op":"d",
		"before":{"id":42,"tenant_id":1,"title":"T","body":"B","tier":"bulk","version":5},
		"source":{"ts_ms":1700000000000,"table":"documents"}
	}`)
	d, err := FromDebezium(raw)
	if err != nil {
		t.Fatal(err)
	}
	if d.Op != contract.OpDelete {
		t.Fatalf("want delete, got %s", d.Op)
	}
	if d.Tier != contract.TierUrgent {
		t.Fatalf("deletes must be urgent, got %s", d.Tier)
	}
}

func TestFromDebeziumSnapshotRead(t *testing.T) {
	raw := []byte(`{
		"op":"r",
		"after":{"id":1,"tenant_id":1,"title":"T","body":"B","tier":"bulk","version":1},
		"source":{"ts_ms":1700000000000,"table":"documents"}
	}`)
	d, err := FromDebezium(raw)
	if err != nil {
		t.Fatal(err)
	}
	if d.Op != contract.OpUpsert {
		t.Fatalf("snapshot read should be upsert, got %s", d.Op)
	}
}

func TestFromDebeziumSkipAndErrors(t *testing.T) {
	if _, err := FromDebezium([]byte(`{"op":"t"}`)); err != ErrSkip {
		t.Fatalf("truncate should skip, got %v", err)
	}
	if _, err := FromDebezium([]byte(`{"op":""}`)); err != ErrSkip {
		t.Fatalf("empty op should skip, got %v", err)
	}
	if _, err := FromDebezium([]byte(`not json`)); err == nil {
		t.Fatal("garbage should error")
	}
	if _, err := FromDebezium([]byte(`{"op":"c","after":null}`)); err == nil {
		t.Fatal("create with nil after should error")
	}
	if _, err := FromDebezium([]byte(`{"op":"z","after":{"id":1}}`)); err == nil {
		t.Fatal("unknown op should error")
	}
}
