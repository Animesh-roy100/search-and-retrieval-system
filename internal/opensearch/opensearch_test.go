package opensearch

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/animeshroy/search-and-retrieval-system/internal/contract"
)

func TestBulkUsesExternalGTEForUpsertsAndDeletes(t *testing.T) {
	var requestBody string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/_bulk" {
			t.Fatalf("path = %q, want /_bulk", r.URL.Path)
		}
		body, err := io.ReadAll(r.Body)
		if err != nil {
			t.Fatalf("read request: %v", err)
		}
		requestBody = string(body)
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{
			"took":1,"errors":false,"items":[
				{"index":{"_index":"documents","_id":"doc:1","_version":7,"result":"updated","status":200}},
				{"delete":{"_index":"documents","_id":"doc:2","_version":8,"result":"deleted","status":200}}
			]}`)
	}))
	defer srv.Close()

	client, err := New(srv.URL)
	if err != nil {
		t.Fatalf("new client: %v", err)
	}
	docs := []contract.CanonicalDoc{
		{
			Source: "test", DocID: "doc:1", TenantID: 1, Op: contract.OpUpsert,
			Tier: contract.TierUrgent, Version: 7, CommitTS: time.Now(), Body: "body",
		},
		{
			Source: "test", DocID: "doc:2", TenantID: 1, Op: contract.OpDelete,
			Tier: contract.TierUrgent, Version: 8, CommitTS: time.Now(),
		},
	}

	applied, err := client.Bulk(context.Background(), docs)
	if err != nil {
		t.Fatalf("bulk: %v", err)
	}
	if !applied["doc:1"] || !applied["doc:2"] {
		t.Fatalf("applied = %#v, want both docs", applied)
	}
	if got := strings.Count(requestBody, `"version_type":"external_gte"`); got != 2 {
		t.Fatalf("external_gte count = %d, want 2; body:\n%s", got, requestBody)
	}
	if strings.Contains(requestBody, `"version_type":"external"`) {
		t.Fatalf("strict external versioning would strand Qdrant retries; body:\n%s", requestBody)
	}
}
