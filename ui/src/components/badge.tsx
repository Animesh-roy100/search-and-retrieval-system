import type { HTMLAttributes, ReactNode } from 'react';
import { cva, type VariantProps } from 'class-variance-authority';
import { cn } from '../lib/utils';

const badgeVariants = cva(
  'inline-flex items-center gap-[var(--spacing-4)] rounded-full border px-[var(--spacing-8)] py-[var(--spacing-2)] type-text-xs font-medium',
  {
    variants: {
      tone: {
        neutral: 'bg-surface-raised text-foreground-secondary border-border-strong',
        brand: 'bg-brand-subtle text-foreground-brand border-border-brand',
        success: 'bg-success-subtle text-foreground-success border-transparent',
        warning: 'bg-warning-subtle text-foreground-warning border-transparent',
        danger: 'bg-danger-subtle text-foreground-danger border-border-danger',
        info: 'bg-info-subtle text-foreground-info border-transparent',
      },
    },
    defaultVariants: { tone: 'neutral' },
  },
);

export interface BadgeProps
  extends HTMLAttributes<HTMLSpanElement>,
    VariantProps<typeof badgeVariants> {
  children?: ReactNode;
}

export function Badge({ tone, className, children, ...rest }: BadgeProps) {
  return (
    <span className={cn(badgeVariants({ tone }), className)} {...rest}>
      {children}
    </span>
  );
}
