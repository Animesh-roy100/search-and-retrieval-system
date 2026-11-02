import { useState } from 'react';
import { Search as SearchIcon, FileText, Clock } from 'lucide-react';
import { Typography } from '../components/typography';
import { Button } from '../components/button';
import { Input } from '../components/input';
import { Card, CardBody } from '../components/card';
import { Badge } from '../components/badge';
import { search } from '../api/client';
import { ApiError } from '../api/client';
import type { SearchResponse } from '../api/types';
import { useSettings } from '../hooks/useSettings';
import { originTone } from '../lib/format';

export function SearchPage() {
  const { settings } = useSettings();
  const [query, setQuery] = useState('');
  const [data, setData] = useState<SearchResponse | null>(null);
  const [loading, setLoading] = useState(false);
  const [error, setError] = useState<string | null>(null);

  async function run(e?: React.FormEvent) {
    e?.preventDefault();
    if (!query.trim()) return;
    setLoading(true);
    setError(null);
    try {
      setData(await search({ query }, settings));
    } catch (err) {
      setError(err instanceof ApiError ? err.message : 'Unexpected error');
      setData(null);
    } finally {
      setLoading(false);
    }
  }

  const results = data?.results ?? [];

  return (
    <div className="mx-auto flex max-w-3xl flex-col gap-[var(--spacing-24)]">
      <header className="flex flex-col gap-[var(--spacing-6)]">
        <Typography variant="header-xl" weight="bold">
          Hybrid Search
        </Typography>
        <Typography variant="text-md" className="text-foreground-secondary">
          BM25 + vector retrieval fused with RRF, then cross-encoder reranked.
        </Typography>
      </header>

      <form onSubmit={run} className="flex gap-[var(--spacing-8)]">
        <Input
          iconLeft={<SearchIcon />}
          placeholder="Search your indexed documents…"
          value={query}
          onChange={(e) => setQuery(e.target.value)}
          autoFocus
          className="flex-1"
        />
        <Button type="submit" size="lg" loading={loading} disabled={!query.trim()}>
          Search
        </Button>
      </form>

      {error && (
        <Card className="border-border-danger">
          <CardBody>
            <Typography variant="text-sm" className="text-foreground-danger">
              {error}
            </Typography>
          </CardBody>
        </Card>
      )}

      {data && (
        <div className="flex items-center gap-[var(--spacing-12)]">
          <Typography variant="text-sm" className="text-foreground-tertiary">
            {results.length} result{results.length === 1 ? '' : 's'}
          </Typography>
          <Badge tone="neutral">
            <Clock className="size-3" />
            {data.latency_ms} ms
          </Badge>
        </div>
      )}

      <div className="flex flex-col gap-[var(--spacing-12)]">
        {results.map((r, i) => (
          <Card key={r.doc_id} interactive>
            <CardBody className="flex flex-col gap-[var(--spacing-8)]">
              <div className="flex items-center justify-between gap-[var(--spacing-8)]">
                <div className="flex items-center gap-[var(--spacing-8)]">
                  <span className="flex size-6 items-center justify-center rounded-md bg-brand-subtle type-text-xs font-semibold text-foreground-brand">
                    {i + 1}
                  </span>
                  <Typography variant="text-sm" weight="semibold" className="font-mono text-foreground-secondary">
                    {r.doc_id}
                  </Typography>
                </div>
                <Badge tone={originTone(r.origin)}>
                  <FileText className="size-3" />
                  {r.origin}
                </Badge>
              </div>
              <Typography variant="text-md" className="text-foreground">
                {r.text}
              </Typography>
            </CardBody>
          </Card>
        ))}
        {data && results.length === 0 && !error && (
          <Card>
            <CardBody>
              <Typography variant="text-md" className="text-foreground-secondary">
                No documents matched. Try a different query, or seed data with{' '}
                <code className="font-mono text-foreground-brand">make demo</code>.
              </Typography>
            </CardBody>
          </Card>
        )}
      </div>
    </div>
  );
}
