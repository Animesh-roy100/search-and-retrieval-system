import { useEffect, useState } from 'react';
import { NavLink, Outlet } from 'react-router-dom';
import { Gem, Search, Sparkles, Activity, Settings as SettingsIcon, X } from 'lucide-react';
import { Typography } from '../components/typography';
import { Button } from '../components/button';
import { Input } from '../components/input';
import { Badge } from '../components/badge';
import { useSettings } from '../hooks/useSettings';
import { health } from '../api/client';
import { cn } from '../lib/utils';

const NAV = [
  { to: '/', label: 'Search', icon: Search, end: true },
  { to: '/ask', label: 'Ask (RAG)', icon: Sparkles, end: false },
  { to: '/ops', label: 'Health', icon: Activity, end: false },
];

export function AppShell() {
  const { settings, update } = useSettings();
  const [open, setOpen] = useState(false);
  const [up, setUp] = useState<boolean | null>(null);

  useEffect(() => {
    let alive = true;
    const ping = () => health().then((ok) => alive && setUp(ok));
    ping();
    const id = setInterval(ping, 15000);
    return () => {
      alive = false;
      clearInterval(id);
    };
  }, []);

  return (
    <div className="flex min-h-screen bg-background">
      {/* Sidebar — brand-tinted shell */}
      <aside className="flex w-60 shrink-0 flex-col border-r border-border bg-shell-tint px-[var(--spacing-16)] py-[var(--spacing-20)]">
        <div className="mb-[var(--spacing-32)] flex items-center gap-[var(--spacing-8)] px-[var(--spacing-8)]">
          <div className="flex size-8 items-center justify-center rounded-lg bg-brand text-foreground-on-brand">
            <Gem className="size-5" />
          </div>
          <Typography variant="header-sm" weight="bold" className="font-heading">
            PrismSearch
          </Typography>
        </div>
        <nav className="flex flex-col gap-[var(--spacing-2)]">
          {NAV.map(({ to, label, icon: Icon, end }) => (
            <NavLink
              key={to}
              to={to}
              end={end}
              className={({ isActive }) =>
                cn(
                  'flex items-center gap-[var(--spacing-8)] rounded-md px-[var(--spacing-12)] py-[var(--spacing-8)] type-text-sm font-medium transition-colors',
                  isActive
                    ? 'bg-selected text-foreground-brand'
                    : 'text-foreground-secondary hover:bg-hover hover:text-foreground',
                )
              }
            >
              <Icon className="size-4" />
              {label}
            </NavLink>
          ))}
        </nav>
        <div className="mt-auto px-[var(--spacing-8)]">
          <Typography variant="text-xs" className="text-foreground-tertiary">
            Hybrid retrieval · BM25 + vector + RRF + rerank · grounded RAG
          </Typography>
        </div>
      </aside>

      {/* Main column */}
      <div className="flex min-w-0 flex-1 flex-col">
        <header className="flex items-center justify-between border-b border-border bg-shell-tint px-[var(--spacing-24)] py-[var(--spacing-12)]">
          <div className="flex items-center gap-[var(--spacing-12)]">
            <Badge tone={up === null ? 'neutral' : up ? 'success' : 'danger'}>
              <span
                className={cn(
                  'size-2 rounded-full',
                  up === null ? 'bg-current' : up ? 'bg-current' : 'bg-current',
                )}
              />
              {up === null ? 'Checking…' : up ? 'API online' : 'API offline'}
            </Badge>
            {settings.tenant && <Badge tone="brand">tenant: {settings.tenant}</Badge>}
          </div>
          <Button variant="ghost" size="sm" iconLeft={<SettingsIcon />} onClick={() => setOpen(true)}>
            Settings
          </Button>
        </header>

        <main className="min-w-0 flex-1 overflow-y-auto px-[var(--spacing-24)] py-[var(--spacing-24)]">
          <Outlet />
        </main>
      </div>

      {open && <SettingsDrawer onClose={() => setOpen(false)} settings={settings} update={update} />}
    </div>
  );
}

function SettingsDrawer({
  onClose,
  settings,
  update,
}: {
  onClose: () => void;
  settings: { tenant: string; apiKey: string };
  update: (patch: Partial<{ tenant: string; apiKey: string }>) => void;
}) {
  return (
    <div className="fixed inset-0 z-50 flex justify-end bg-black/50" onClick={onClose}>
      <div
        className="flex h-full w-96 flex-col gap-[var(--spacing-20)] border-l border-border bg-surface p-[var(--spacing-24)] shadow-float"
        onClick={(e) => e.stopPropagation()}
      >
        <div className="flex items-center justify-between">
          <Typography variant="header-md" weight="semibold">
            Connection
          </Typography>
          <Button variant="ghost" size="sm" iconOnly iconLeft={<X />} onClick={onClose} aria-label="Close" />
        </div>
        <Typography variant="text-sm" className="text-foreground-secondary">
          When the query service sets <code className="font-mono text-foreground-brand">QUERY_API_KEY</code>,
          requests must send an API key and a tenant id. Leave blank in open dev mode.
        </Typography>
        <label className="flex flex-col gap-[var(--spacing-6)]">
          <Typography variant="text-sm" weight="medium">
            Tenant ID
          </Typography>
          <Input
            value={settings.tenant}
            placeholder="e.g. 1"
            onChange={(e) => update({ tenant: e.target.value })}
          />
        </label>
        <label className="flex flex-col gap-[var(--spacing-6)]">
          <Typography variant="text-sm" weight="medium">
            API Key
          </Typography>
          <Input
            type="password"
            value={settings.apiKey}
            placeholder="X-API-Key"
            onChange={(e) => update({ apiKey: e.target.value })}
          />
        </label>
        <Button variant="primary" block onClick={onClose}>
          Done
        </Button>
      </div>
    </div>
  );
}
