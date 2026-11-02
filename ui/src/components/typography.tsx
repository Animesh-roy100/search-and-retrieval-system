import type { ElementType, HTMLAttributes, ReactNode } from 'react';
import { cn } from '../lib/utils';

export type TypographyVariant =
  | 'text-xs'
  | 'text-sm'
  | 'text-md'
  | 'text-lg'
  | 'header-sm'
  | 'header-md'
  | 'header-lg'
  | 'header-xl';

export type TypographyWeight = 'light' | 'regular' | 'medium' | 'semibold' | 'bold';

const WEIGHT_CLASS: Record<TypographyWeight, string> = {
  light: 'font-light',
  regular: 'font-normal',
  medium: 'font-medium',
  semibold: 'font-semibold',
  bold: 'font-bold',
};

const DEFAULT_TAG: Record<TypographyVariant, ElementType> = {
  'header-xl': 'h1',
  'header-lg': 'h2',
  'header-md': 'h3',
  'header-sm': 'h4',
  'text-lg': 'p',
  'text-md': 'p',
  'text-sm': 'p',
  'text-xs': 'p',
};

export interface TypographyProps extends HTMLAttributes<HTMLElement> {
  variant: TypographyVariant;
  weight?: TypographyWeight;
  as?: ElementType;
  children?: ReactNode;
}

/** Renders text via a design-token type role (size + line-height + family). */
export function Typography({ variant, weight, as, className, children, ...rest }: TypographyProps) {
  const Tag = as ?? DEFAULT_TAG[variant];
  const isHeader = variant.startsWith('header-');
  const w = weight ?? (isHeader ? 'semibold' : 'regular');
  const family = isHeader ? 'font-heading' : 'font-sans';
  return (
    <Tag className={cn(`type-${variant}`, family, WEIGHT_CLASS[w], className)} {...rest}>
      {children}
    </Tag>
  );
}
