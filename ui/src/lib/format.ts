import type { BadgeProps } from '../components/badge';

/** Map a faithfulness score (0–1) to a badge tone + label. */
export function faithfulnessTone(score: number): { tone: BadgeProps['tone']; label: string } {
  if (score >= 0.9) return { tone: 'success', label: 'Grounded' };
  if (score >= 0.6) return { tone: 'warning', label: 'Partial' };
  return { tone: 'danger', label: 'Low' };
}

export function pct(n: number): string {
  return `${Math.round(n * 100)}%`;
}

export function originTone(origin: string): BadgeProps['tone'] {
  switch (origin) {
    case 'postgres':
      return 'info';
    default:
      return 'neutral';
  }
}
