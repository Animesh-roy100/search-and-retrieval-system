// Command loadgen simulates production ingestion by writing to the Postgres source
// (the real front door — everything flows through CDC exactly as in production).
//
// Modes:
//
//	backfill  insert N documents as fast as the target rate allows (bulk onboarding).
//	stream    sustain a fixed writes/sec for a duration (steady-state CDC), mixing
//	          urgent + bulk tiers and inserts + updates.
//
// It writes via multi-row batched INSERTs and reports achieved throughput. Freshness
// and consumer lag are observed separately on the Grafana dashboard / Prometheus.
package main

import (
	"context"
	"database/sql"
	"flag"
	"fmt"
	"log"
	"math/rand"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	_ "github.com/jackc/pgx/v5/stdlib"
)

var (
	categories = []string{"cdc", "streaming", "retrieval", "rag", "indexing", "observability", "vectors", "infra"}
	words      = strings.Fields(`debezium redpanda kafka opensearch qdrant vector bm25 rrf rerank embedding
		faithfulness citation freshness lag idempotency dlq snapshot replication wal consumer partition
		throughput latency backpressure semantic hybrid retrieval grounded chunk tenant upsert tombstone`)
)

func main() {
	var (
		dsn       = flag.String("dsn", "postgresql://retrieval:retrieval@localhost:5433/retrieval", "postgres DSN")
		mode      = flag.String("mode", "stream", "backfill | stream")
		count     = flag.Int("count", 100000, "backfill: total docs to insert")
		rate      = flag.Int("rate", 500, "target writes/sec")
		duration  = flag.Duration("duration", 60*time.Second, "stream: how long to run")
		batch     = flag.Int("batch", 100, "rows per INSERT statement")
		urgentPct = flag.Int("urgent-pct", 20, "percent of writes on the urgent (fast) tier")
		updatePct = flag.Int("update-pct", 0, "stream: percent of writes that update an existing row instead of insert")
		workers   = flag.Int("workers", 4, "concurrent writer goroutines")
	)
	flag.Parse()

	db, err := sql.Open("pgx", *dsn)
	if err != nil {
		log.Fatalf("loadgen: open: %v", err)
	}
	defer db.Close()
	db.SetMaxOpenConns(*workers + 2)
	if err := db.PingContext(context.Background()); err != nil {
		log.Fatalf("loadgen: ping (is the stack up? DSN=%s): %v", *dsn, err)
	}

	// discover current max id for update mode
	var maxID int64
	_ = db.QueryRow("SELECT COALESCE(MAX(id),0) FROM documents").Scan(&maxID)

	cfg := runConfig{
		db: db, rate: *rate, batch: *batch, urgentPct: *urgentPct,
		updatePct: *updatePct, workers: *workers, maxID: maxID,
	}

	switch *mode {
	case "backfill":
		cfg.backfill(*count)
	case "stream":
		cfg.stream(*duration)
	default:
		log.Fatalf("loadgen: unknown mode %q", *mode)
	}
}

type runConfig struct {
	db        *sql.DB
	rate      int
	batch     int
	urgentPct int
	updatePct int
	workers   int
	maxID     int64
	inserted  atomic.Int64
	updated   atomic.Int64
}

// backfill inserts total docs, rate-limited, across workers, reporting throughput.
func (c *runConfig) backfill(total int) {
	log.Printf("loadgen backfill: %d docs @ ~%d/s in batches of %d (%d workers)", total, c.rate, c.batch, c.workers)
	start := time.Now()
	jobs := make(chan int, c.workers*2)
	var wg sync.WaitGroup
	lim := newLimiter(c.rate)

	for w := 0; w < c.workers; w++ {
		wg.Add(1)
		go func(seed int64) {
			defer wg.Done()
			rng := rand.New(rand.NewSource(seed))
			for n := range jobs {
				lim.waitN(n)
				if err := c.insertBatch(rng, n); err != nil {
					log.Printf("loadgen: insert batch: %v", err)
					continue
				}
				c.inserted.Add(int64(n))
			}
		}(int64(w + 1))
	}

	go c.progress(start, total)

	remaining := total
	for remaining > 0 {
		b := c.batch
		if b > remaining {
			b = remaining
		}
		jobs <- b
		remaining -= b
	}
	close(jobs)
	wg.Wait()

	elapsed := time.Since(start)
	log.Printf("loadgen backfill DONE: %d docs in %s = %.0f docs/s",
		c.inserted.Load(), elapsed.Round(time.Millisecond), float64(c.inserted.Load())/elapsed.Seconds())
}

// stream sustains rate writes/sec for duration, mixing inserts/updates + tiers.
func (c *runConfig) stream(dur time.Duration) {
	log.Printf("loadgen stream: ~%d writes/s for %s (urgent %d%%, update %d%%)",
		c.rate, dur, c.urgentPct, c.updatePct)
	start := time.Now()
	deadline := start.Add(dur)
	lim := newLimiter(c.rate)
	var wg sync.WaitGroup

	for w := 0; w < c.workers; w++ {
		wg.Add(1)
		go func(seed int64) {
			defer wg.Done()
			rng := rand.New(rand.NewSource(seed))
			for time.Now().Before(deadline) {
				lim.waitN(c.batch)
				if c.updatePct > 0 && rng.Intn(100) < c.updatePct && c.maxID > 0 {
					if err := c.updateBatch(rng, c.batch); err != nil {
						log.Printf("loadgen: update: %v", err)
					} else {
						c.updated.Add(int64(c.batch))
					}
					continue
				}
				if err := c.insertBatch(rng, c.batch); err != nil {
					log.Printf("loadgen: insert: %v", err)
				} else {
					c.inserted.Add(int64(c.batch))
				}
			}
		}(int64(w + 100))
	}

	go c.progress(start, 0)
	wg.Wait()
	elapsed := time.Since(start)
	total := c.inserted.Load() + c.updated.Load()
	log.Printf("loadgen stream DONE: %d writes (%d ins / %d upd) in %s = %.0f writes/s",
		total, c.inserted.Load(), c.updated.Load(), elapsed.Round(time.Millisecond), float64(total)/elapsed.Seconds())
}

func (c *runConfig) insertBatch(rng *rand.Rand, n int) error {
	var sb strings.Builder
	sb.WriteString("INSERT INTO documents (tenant_id,title,body,category,tier) VALUES ")
	args := make([]any, 0, n*5)
	for i := 0; i < n; i++ {
		if i > 0 {
			sb.WriteByte(',')
		}
		base := i * 5
		fmt.Fprintf(&sb, "($%d,$%d,$%d,$%d,$%d)", base+1, base+2, base+3, base+4, base+5)
		tier := "bulk"
		if rng.Intn(100) < c.urgentPct {
			tier = "urgent"
		}
		args = append(args,
			int64(rng.Intn(10)+1),          // tenant_id
			c.title(rng),                   // title
			c.body(rng),                    // body
			categories[rng.Intn(len(categories))],
			tier,
		)
	}
	_, err := c.db.Exec(sb.String(), args...)
	return err
}

func (c *runConfig) updateBatch(rng *rand.Rand, n int) error {
	// Touch n random existing rows; the version-bump trigger makes each a new CDC event.
	for i := 0; i < n; i++ {
		id := rng.Int63n(c.maxID) + 1
		if _, err := c.db.Exec(
			"UPDATE documents SET body=$1 WHERE id=$2", c.body(rng), id,
		); err != nil {
			return err
		}
	}
	return nil
}

func (c *runConfig) title(rng *rand.Rand) string {
	w1 := words[rng.Intn(len(words))]
	w2 := words[rng.Intn(len(words))]
	if len(w1) > 0 {
		w1 = strings.ToUpper(w1[:1]) + w1[1:]
	}
	return w1 + " " + w2
}

func (c *runConfig) body(rng *rand.Rand) string {
	var b strings.Builder
	for i := 0; i < 30; i++ {
		if i > 0 {
			b.WriteByte(' ')
		}
		b.WriteString(words[rng.Intn(len(words))])
	}
	return b.String()
}

func (c *runConfig) progress(start time.Time, total int) {
	t := time.NewTicker(5 * time.Second)
	defer t.Stop()
	for range t.C {
		done := c.inserted.Load() + c.updated.Load()
		el := time.Since(start).Seconds()
		if total > 0 {
			log.Printf("  ... %d/%d (%.0f docs/s)", done, total, float64(done)/el)
		} else {
			log.Printf("  ... %d writes (%.0f writes/s)", done, float64(done)/el)
		}
	}
}

// limiter is a simple token-bucket-ish rate limiter granting `rate` units/sec.
type limiter struct {
	mu       sync.Mutex
	rate     float64
	allow    float64
	last     time.Time
}

func newLimiter(rate int) *limiter {
	return &limiter{rate: float64(rate), allow: float64(rate), last: time.Now()}
}

func (l *limiter) waitN(n int) {
	if l.rate <= 0 {
		return
	}
	for {
		l.mu.Lock()
		now := time.Now()
		l.allow += l.rate * now.Sub(l.last).Seconds()
		if l.allow > l.rate {
			l.allow = l.rate
		}
		l.last = now
		if l.allow >= float64(n) {
			l.allow -= float64(n)
			l.mu.Unlock()
			return
		}
		deficit := float64(n) - l.allow
		l.mu.Unlock()
		time.Sleep(time.Duration(deficit / l.rate * float64(time.Second)))
	}
}
