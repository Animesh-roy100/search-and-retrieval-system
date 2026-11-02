package main

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"testing"

	"github.com/twmb/franz-go/pkg/kgo"

	"github.com/animeshroy/search-and-retrieval-system/internal/idempotency"
)

type fakeSyncProducer struct {
	fail    bool
	calls   int
	records []*kgo.Record
}

func (p *fakeSyncProducer) ProduceSync(_ context.Context, records ...*kgo.Record) kgo.ProduceResults {
	p.calls++
	p.records = append(p.records, records...)
	results := make(kgo.ProduceResults, 0, len(records))
	for _, record := range records {
		var err error
		if p.fail {
			err = errors.New("injected DLQ outage")
		}
		results = append(results, kgo.ProduceResult{Record: record, Err: err})
	}
	return results
}

func TestPoisonOffsetCommittedOnlyAfterDLQPublication(t *testing.T) {
	producer := &fakeSyncProducer{fail: true}
	ix := &indexer{guard: idempotency.NewGuard(), prod: producer, dlq: "docs.dlq"}
	record := &kgo.Record{Topic: "docs.fast", Partition: 2, Offset: 41, Value: []byte("not-json")}
	batch := &pendingBatch{
		records: []*kgo.Record{record},
		poisons: []poisonRecord{{original: record.Value, reason: "decode_error"}},
	}
	commits := 0
	commit := func(_ context.Context, records ...*kgo.Record) error {
		commits++
		if len(records) != 1 || records[0].Offset != 41 {
			t.Fatalf("committed records = %#v", records)
		}
		return nil
	}

	if err := ix.flushPending(context.Background(), batch, "urgent", commit); err == nil {
		t.Fatal("flush should fail while DLQ publication fails")
	}
	if commits != 0 {
		t.Fatalf("commits = %d, poison offset must remain uncommitted", commits)
	}
	if batch.nextPoison != 0 {
		t.Fatalf("nextPoison = %d, failed publication must be retried", batch.nextPoison)
	}

	producer.fail = false
	if err := ix.flushPending(context.Background(), batch, "urgent", commit); err != nil {
		t.Fatalf("retry flush: %v", err)
	}
	if commits != 1 {
		t.Fatalf("commits = %d, want 1 after DLQ success", commits)
	}
	if batch.nextPoison != 1 {
		t.Fatalf("nextPoison = %d, want 1", batch.nextPoison)
	}

	var envelope map[string]any
	if err := json.Unmarshal(producer.records[len(producer.records)-1].Value, &envelope); err != nil {
		t.Fatalf("DLQ envelope must be valid JSON: %v", err)
	}
	wantB64 := base64.StdEncoding.EncodeToString(record.Value)
	if envelope["original_b64"] != wantB64 {
		t.Fatalf("original_b64 = %#v, want %q", envelope["original_b64"], wantB64)
	}
}

func TestCommitRetryDoesNotRepublishSuccessfulDLQRecord(t *testing.T) {
	producer := &fakeSyncProducer{}
	ix := &indexer{guard: idempotency.NewGuard(), prod: producer, dlq: "docs.dlq"}
	record := &kgo.Record{Topic: "docs.fast", Partition: 0, Offset: 9, Value: []byte(`{"bad":true}`)}
	batch := &pendingBatch{
		records: []*kgo.Record{record},
		poisons: []poisonRecord{{original: record.Value, reason: "decode_error"}},
	}
	commitCalls := 0
	commit := func(context.Context, ...*kgo.Record) error {
		commitCalls++
		if commitCalls == 1 {
			return errors.New("injected commit failure")
		}
		return nil
	}

	if err := ix.flushPending(context.Background(), batch, "urgent", commit); err == nil {
		t.Fatal("first commit should fail")
	}
	if err := ix.flushPending(context.Background(), batch, "urgent", commit); err != nil {
		t.Fatalf("commit retry: %v", err)
	}
	if producer.calls != 1 {
		t.Fatalf("DLQ publish calls = %d, want 1 across commit retry", producer.calls)
	}
	if commitCalls != 2 {
		t.Fatalf("commit calls = %d, want 2", commitCalls)
	}
}
