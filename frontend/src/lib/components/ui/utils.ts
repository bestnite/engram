import { clsx, type ClassValue } from 'clsx';
import { twMerge } from 'tailwind-merge';

/**
 * cn merges Tailwind class names: clsx handles conditional expressions and tailwind-merge resolves conflicts between utilities of the same kind
 * (the later one wins, e.g. an externally passed `rounded-lg` overrides a component's default `rounded-xl`).
 *
 * This is the foundation for "styles reused in several places get wrapped in a component": component defaults + caller overrides go through one path,
 * and pages no longer repeat the same set of class names.
 */
export function cn(...inputs: ClassValue[]): string {
  return twMerge(clsx(inputs));
}
