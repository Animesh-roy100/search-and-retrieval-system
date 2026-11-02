import type { ButtonHTMLAttributes, ReactNode, Ref } from 'react';
import { cva, type VariantProps } from 'class-variance-authority';
import { Loader2 } from 'lucide-react';
import { cn } from '../lib/utils';

/** Button — mirrors the sketch Button spec: cva variants painted with token classes,
 *  emergent height from padding + line-height (no hardcoded h-*). */
const buttonVariants = cva(
  'relative inline-flex items-center justify-center whitespace-nowrap font-medium transition-colors border outline-none focus-visible:ring-2 focus-visible:ring-[var(--border-focus)] disabled:cursor-not-allowed disabled:opacity-50',
  {
    variants: {
      variant: {
        primary: 'bg-brand hover:bg-brand-hover text-foreground-on-brand border-border-brand',
        default: 'bg-surface-raised hover:bg-hover text-foreground border-border-strong',
        outline: 'bg-transparent hover:bg-hover text-foreground border-border-strong',
        ghost: 'bg-transparent hover:bg-hover text-foreground-secondary border-transparent',
        danger:
          'bg-danger-subtle hover:bg-danger-subtle text-foreground-danger border-border-danger',
      },
      size: {
        sm: 'px-[var(--spacing-8)] py-[var(--spacing-4)] gap-[var(--spacing-4)] rounded-md type-text-sm [&_svg]:size-4',
        md: 'px-[var(--spacing-12)] py-[var(--spacing-6)] gap-[var(--spacing-6)] rounded-md type-text-sm [&_svg]:size-4',
        lg: 'px-[var(--spacing-16)] py-[var(--spacing-8)] gap-[var(--spacing-6)] rounded-lg type-text-md [&_svg]:size-5',
      },
      iconOnly: { true: 'aspect-square px-0', false: '' },
      block: { true: 'w-full', false: '' },
    },
    defaultVariants: { variant: 'primary', size: 'md', iconOnly: false, block: false },
  },
);

export interface ButtonProps
  extends ButtonHTMLAttributes<HTMLButtonElement>,
    Omit<VariantProps<typeof buttonVariants>, 'iconOnly' | 'block'> {
  ref?: Ref<HTMLButtonElement>;
  loading?: boolean;
  iconLeft?: ReactNode;
  iconRight?: ReactNode;
  iconOnly?: boolean;
  block?: boolean;
}

export function Button({
  ref,
  variant,
  size,
  type = 'button',
  loading = false,
  iconLeft,
  iconRight,
  iconOnly = false,
  block = false,
  disabled,
  className,
  children,
  ...rest
}: ButtonProps) {
  const isDisabled = disabled || loading;
  const leftIcon = loading ? <Loader2 className="animate-spin" /> : iconLeft;
  return (
    <button
      ref={ref}
      type={type}
      disabled={isDisabled}
      aria-busy={loading || undefined}
      className={cn(buttonVariants({ variant, size, iconOnly, block }), className)}
      {...rest}
    >
      {leftIcon}
      {children}
      {iconRight}
    </button>
  );
}
