import { Fragment, useState } from 'react';
import { Sparkles, ShieldCheck, Clock, Database, Zap } from 'lucide-react';
import { Typography } from '../components/typography';
import { Button } from '../components/button';
import { Input } from '../components/input';
import { Card, CardBody } from '../components/card';
import { Badge } from '../components/badge';
import { ask, ApiError } from '../api/client';
import type { AskResponse } from '../api/types';
import { useSettings } from '../hooks/useSettings';
import { faithfulnessTone, pct, originTone } from '../lib/format';

/** Split an answer on [doc_N] markers so each marker renders as an inline chip. */
function renderWithCitations(answer: string) {
  const parts = answer.split(/(\[doc_\d+\])/g);
  return parts.map((part, i) => {
    const m = part.match(/^\[(doc_\d+)\]$/);
    if (m) {
      return (
        <a
          key={i}
          href={`#${m[1]}`}
          className="mx-[2px] inline-flex items-center rounded bg-brand-subtle px-[var(--spacing-4)] type-text-xs font-semibold text-foreground-brand align-middle no-underline hover:bg-selected"
        >
          {m[1]}
        </a>
      );
    }
    return <Fragment key={i}>{part}</Fragment>;
  });
}

export function AskPage() {
  const { settings } = useSettings();
  const [query, setQuery] = useState('');
  const [data, setData] = useState<AskResponse | null>(null);
  const [loading, setLoading] = useState(false);
  const [error, setError] = useState<string | null>(null);

  async function run(e?: React.FormEvent) {
    e?.preventDefault();
    if (!query.trim()) return;
    setLoading(true);
    setError(null);
    try {
      setData(await ask({ query }, settings));
    } catch (err) {
      setError(err instanceof ApiError ? err.message : 'Unexpected error');
      setData(null);
    } finally {
      setLoading(false);
    }
  }

  const faith = typeof data?.faithfulness === 'number' ? data.faithfulness : null;
  const ft = faith !== null ? faithfulnessTone(faith) : null;
  const citations = data?.citations ?? [];
  const degradedSources = data?.results ?? [];

  return (
    <div className="mx-auto flex max-w-3xl flex-col gap-[var(--spacing-24)]">
      <header className="flex flex-col gap-[var(--spacing-6)]">
        <Typography variant="header-xl" weight="bold">
          Ask — Grounded RAG
        </Typography>
        <Typography variant="text-md" className="text-foreground-secondary">
          Retrieved context, a cited answer, and a faithfulness guardrail on every response.
        </Typography>
      </header>

      <form onSubmit={run} className="flex gap-[var(--spacing-8)]">
        <Input
          iconLeft={<Sparkles />}
          placeholder="Ask a question about your documents…"
          value={query}
          onChange={(e) => setQuery(e.target.value)}
          autoFocus
          className="flex-1"
        />
        <Button type="submit" size="lg" loading={loading} disabled={!query.trim()}>
          Ask
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

      {data?.degraded && (
        <Card className="border-border-danger">
          <CardBody>
            <Typography variant="text-sm" className="text-foreground-warning">
              LLM unavailable — showing retrieved documents only (graceful degradation).
            </Typography>
          </CardBody>
        </Card>
      )}

      {data && data.answer && (
        <Card>
          <CardBody className="flex flex-col gap-[var(--spacing-16)]">
            <div className="flex flex-wrap items-center gap-[var(--spacing-8)]">
              {ft && faith !== null && (
                <Badge tone={ft.tone}>
                  <ShieldCheck className="size-3" />
                  Faithfulness {pct(faith)} · {ft.label}
                </Badge>
              )}
              {data.model && <Badge tone="neutral">{data.model}</Badge>}
              {typeof data.latency_ms === 'number' && (
                <Badge tone="neutral">
                  <Clock className="size-3" />
                  {data.latency_ms} ms
                </Badge>
              )}
              {data.cached && (
                <Badge tone="info">
                  <Zap className="size-3" />
                  {data.semantic ? `semantic cache ${pct(data.semantic_score ?? 0)}` : 'cached'}
                </Badge>
              )}
            </div>

            <Typography variant="text-lg" className="leading-relaxed text-foreground">
              {renderWithCitations(data.answer)}
            </Typography>

            {data.warning && (
              <Typography variant="text-sm" className="text-foreground-warning">
                ⚠ {data.warning}
              </Typography>
            )}
          </CardBody>
        </Card>
      )}

      {(citations.length > 0 || degradedSources.length > 0) && (
        <div className="flex flex-col gap-[var(--spacing-12)]">
          <Typography variant="header-sm" weight="semibold" className="flex items-center gap-[var(--spacing-8)]">
            <Database className="size-4 text-foreground-tertiary" />
            Sources
          </Typography>

          {citations.map((c) => (
            <Card key={c.marker} id={c.marker} interactive>
              <CardBody className="flex items-center justify-between gap-[var(--spacing-8)]">
                <div className="flex items-center gap-[var(--spacing-8)]">
                  <Badge tone="brand">{c.marker}</Badge>
                  <Typography variant="text-sm" weight="medium" className="font-mono text-foreground-secondary">
                    {c.doc_id}
                  </Typography>
                </div>
                <div className="flex items-center gap-[var(--spacing-8)]">
                  {typeof c.score === 'number' && (
                    <Badge tone="neutral">score {c.score.toFixed(3)}</Badge>
                  )}
                  {c.source && <Badge tone={originTone(c.source)}>{c.source}</Badge>}
                </div>
              </CardBody>
            </Card>
          ))}

          {degradedSources.map((r) => (
            <Card key={r.doc_id} id={r.marker} interactive>
              <CardBody className="flex flex-col gap-[var(--spacing-8)]">
                <div className="flex items-center justify-between">
                  <Badge tone="brand">{r.marker}</Badge>
                  <Badge tone={originTone(r.origin)}>{r.origin}</Badge>
                </div>
                <Typography variant="text-sm" className="text-foreground-secondary">
                  {r.text}
                </Typography>
              </CardBody>
            </Card>
          ))}
        </div>
      )}
    </div>
  );
}
