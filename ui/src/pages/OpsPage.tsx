import { useEffect, useState } from 'react';
import { Activity, CheckCircle2, XCircle, RefreshCw, Boxes } from 'lucide-react';
import { Typography } from '../components/typography';
import { Button } from '../components/button';
import { Card, CardBody } from '../components/card';
import { Badge } from '../components/badge';
import { health } from '../api/client';

interface Probe {
  up: boolean | null;
  latencyMs: number | null;
  checkedAt: string | null;
}

const STAGES = [
  { name: 'BM25 (OpenSearch)', note: 'lexical lane', tone: 'info' as const },
  { name: 'Vector (Qdrant)', note: 'semantic lane', tone: 'info' as const },
  { name: 'RRF fusion', note: 'reciprocal-rank fusion', tone: 'brand' as const },
  { name: 'Cross-encoder rerank', note: 'ML service', tone: 'brand' as const },
  { name: 'Grounded RAG', note: 'cited + faithfulness gate', tone: 'success' as const },
];

export function OpsPage() {
  const [probe, setProbe] = useState<Probe>({ up: null, latencyMs: null, checkedAt: null });
  const [loading, setLoading] = useState(false);

  async function check() {
    setLoading(true);
    const t0 = performance.now();
    const up = await health();
    const latencyMs = Math.round(performance.now() - t0);
    setProbe({ up, latencyMs, checkedAt: new Date().toLocaleTimeString() });
    setLoading(false);
  }

  useEffect(() => {
    check();
  }, []);

  return (
    <div className="mx-auto flex max-w-3xl flex-col gap-[var(--spacing-24)]">
      <header className="flex items-center justify-between">
        <div className="flex flex-col gap-[var(--spacing-6)]">
          <Typography variant="header-xl" weight="bold">
            System Health
          </Typography>
          <Typography variant="text-md" className="text-foreground-secondary">
            Live status of the query service and the retrieval pipeline.
          </Typography>
        </div>
        <Button variant="default" iconLeft={<RefreshCw />} loading={loading} onClick={check}>
          Refresh
        </Button>
      </header>

      <div className="grid grid-cols-1 gap-[var(--spacing-16)] sm:grid-cols-3">
        <StatCard
          label="Query API"
          value={probe.up === null ? '—' : probe.up ? 'Online' : 'Offline'}
          tone={probe.up ? 'success' : probe.up === false ? 'danger' : 'neutral'}
          icon={probe.up ? <CheckCircle2 /> : <XCircle />}
        />
        <StatCard
          label="Health latency"
          value={probe.latencyMs !== null ? `${probe.latencyMs} ms` : '—'}
          tone="neutral"
          icon={<Activity />}
        />
        <StatCard label="Last checked" value={probe.checkedAt ?? '—'} tone="neutral" icon={<RefreshCw />} />
      </div>

      <Card>
        <CardBody className="flex flex-col gap-[var(--spacing-16)]">
          <Typography variant="header-sm" weight="semibold" className="flex items-center gap-[var(--spacing-8)]">
            <Boxes className="size-4 text-foreground-tertiary" />
            Retrieval pipeline
          </Typography>
          <div className="flex flex-col gap-[var(--spacing-8)]">
            {STAGES.map((s, i) => (
              <div
                key={s.name}
                className="flex items-center gap-[var(--spacing-12)] rounded-md border border-border bg-surface-raised px-[var(--spacing-16)] py-[var(--spacing-12)]"
              >
                <span className="flex size-6 shrink-0 items-center justify-center rounded-full bg-brand-subtle type-text-xs font-semibold text-foreground-brand">
                  {i + 1}
                </span>
                <Typography variant="text-md" weight="medium" className="flex-1">
                  {s.name}
                </Typography>
                <Badge tone={s.tone}>{s.note}</Badge>
              </div>
            ))}
          </div>
          <Typography variant="text-sm" className="text-foreground-tertiary">
            Full Prometheus metrics and Grafana dashboards are exposed by the stack at{' '}
            <code className="font-mono text-foreground-brand">:9090</code> /{' '}
            <code className="font-mono text-foreground-brand">:3000</code>.
          </Typography>
        </CardBody>
      </Card>
    </div>
  );
}

function StatCard({
  label,
  value,
  tone,
  icon,
}: {
  label: string;
  value: string;
  tone: 'success' | 'danger' | 'neutral';
  icon: React.ReactNode;
}) {
  const toneClass =
    tone === 'success'
      ? 'text-foreground-success'
      : tone === 'danger'
        ? 'text-foreground-danger'
        : 'text-foreground-tertiary';
  return (
    <Card>
      <CardBody className="flex flex-col gap-[var(--spacing-8)]">
        <div className="flex items-center justify-between">
          <Typography variant="text-sm" className="text-foreground-tertiary">
            {label}
          </Typography>
          <span className={`[&_svg]:size-4 ${toneClass}`}>{icon}</span>
        </div>
        <Typography variant="header-md" weight="semibold">
          {value}
        </Typography>
      </CardBody>
    </Card>
  );
}
