import type { HTMLAttributes, ReactNode } from 'react';
import { cn } from '../lib/utils';

export function Card({
  className,
  children,
  interactive = false,
  ...rest
}: HTMLAttributes<HTMLDivElement> & { interactive?: boolean; children?: ReactNode }) {
  return (
    <div
      className={cn(
        'rounded-lg border border-border bg-surface shadow-card',
        interactive && 'transition-colors hover:border-border-strong hover:bg-surface-raised',
        className,
      )}
      {...rest}
    >
      {children}
    </div>
  );
}

export function CardBody({ className, children, ...rest }: HTMLAttributes<HTMLDivElement>) {
  return (
    <div className={cn('p-[var(--spacing-20)]', className)} {...rest}>
      {children}
    </div>
  );
}
