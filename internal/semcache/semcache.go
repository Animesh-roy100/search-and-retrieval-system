// Package semcache is a semantic answer cache backed by a dedicated Qdrant
// collection. Unlike the exact-match Redis cache, it matches paraphrases: the
// query embedding is looked up by nearest-neighbor and counts as a hit only above
// a cosine threshold. Entries are scoped by tenant and cache generation, so a write
// that bumps the generation makes prior entries unreachable (freshness-safe).
//
// It degrades gracefully: if disabled or Qdrant is unavailable, Lookup misses and
// Store is a no-op, so the read path is unaffected.
package semcache

import (
	"context"
	"fmt"
	"strconv"

	"github.com/google/uuid"
	qc "github.com/qdrant/go-client/qdrant"
)

const Collection = "answer_cache"

type Cache struct {
	c         *qc.Client
	threshold float64
	enabled   bool
}

// New connects to Qdrant for the answer-cache collection. enabled=false yields a
// usable no-op cache.
func New(host string, port int, threshold float64, enabled bool) (*Cache, error) {
	if !enabled {
		return &Cache{enabled: false}, nil
	}
	cl, err := qc.NewClient(&qc.Config{Host: host, Port: port})
	if err != nil {
		return nil, fmt.Errorf("semcache: connect: %w", err)
	}
	return &Cache{c: cl, threshold: threshold, enabled: true}, nil
}

// Ensure creates the answer-cache collection with the embedding dimension if absent.
func (c *Cache) Ensure(ctx context.Context, dim int) error {
	if !c.enabled {
		return nil
	}
	exists, err := c.c.CollectionExists(ctx, Collection)
	if err != nil {
		return fmt.Errorf("semcache: exists: %w", err)
	}
	if exists {
		return nil
	}
	return c.c.CreateCollection(ctx, &qc.CreateCollection{
		CollectionName: Collection,
		VectorsConfig:  qc.NewVectorsConfig(&qc.VectorParams{Size: uint64(dim), Distance: qc.Distance_Cosine}),
	})
}

// Lookup returns the cached answer JSON for a semantically-similar prior query in
// the same tenant + generation, if one exists above the cosine threshold.
func (c *Cache) Lookup(ctx context.Context, vec []float32, tenant string, gen int64) (string, float64, bool) {
	if !c.enabled || len(vec) == 0 {
		return "", 0, false
	}
	limit := uint64(1)
	withPayload := qc.NewWithPayload(true)
	res, err := c.c.Query(ctx, &qc.QueryPoints{
		CollectionName: Collection,
		Query:          qc.NewQuery(vec...),
		Limit:          &limit,
		WithPayload:    withPayload,
		Filter:         &qc.Filter{Must: scope(tenant, gen)},
	})
	if err != nil || len(res) == 0 {
		return "", 0, false
	}
	top := res[0]
	if float64(top.GetScore()) < c.threshold {
		return "", float64(top.GetScore()), false
	}
	ans := ""
	if v, ok := top.GetPayload()["answer_json"]; ok {
		ans = v.GetStringValue()
	}
	if ans == "" {
		return "", float64(top.GetScore()), false
	}
	return ans, float64(top.GetScore()), true
}

// Store saves a query embedding + its answer JSON, scoped by tenant + generation.
func (c *Cache) Store(ctx context.Context, vec []float32, tenant string, gen int64, answerJSON string) {
	if !c.enabled || len(vec) == 0 {
		return
	}
	wait := false // fire-and-forget; caching is best-effort
	_, _ = c.c.Upsert(ctx, &qc.UpsertPoints{
		CollectionName: Collection,
		Wait:           &wait,
		Points: []*qc.PointStruct{{
			Id:      qc.NewIDUUID(uuid.NewString()),
			Vectors: qc.NewVectorsDense(vec),
			Payload: qc.NewValueMap(map[string]any{
				"tenant_id":   tenant,
				"gen":         strconv.FormatInt(gen, 10),
				"answer_json": answerJSON,
			}),
		}},
	})
}

func scope(tenant string, gen int64) []*qc.Condition {
	conds := []*qc.Condition{qc.NewMatch("gen", strconv.FormatInt(gen, 10))}
	if tenant != "" {
		conds = append(conds, qc.NewMatch("tenant_id", tenant))
	}
	return conds
}
