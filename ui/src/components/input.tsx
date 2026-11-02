import type { InputHTMLAttributes, ReactNode, Ref } from 'react';
import { cn } from '../lib/utils';

export interface InputProps extends InputHTMLAttributes<HTMLInputElement> {
  ref?: Ref<HTMLInputElement>;
  iconLeft?: ReactNode;
}

export function Input({ ref, iconLeft, className, ...rest }: InputProps) {
  return (
    <div
      className={cn(
        'flex items-center gap-[var(--spacing-8)] rounded-lg border border-border-strong bg-surface px-[var(--spacing-12)] py-[var(--spacing-8)]',
        'focus-within:border-border-brand focus-within:ring-2 focus-within:ring-[var(--border-focus)]/40 transition-colors',
        className,
      )}
    >
      {iconLeft && <span className="text-foreground-tertiary [&_svg]:size-4">{iconLeft}</span>}
      <input
        ref={ref}
        className="w-full bg-transparent type-text-md text-foreground placeholder:text-foreground-tertiary outline-none"
        {...rest}
      />
    </div>
  );
}
