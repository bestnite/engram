import { tv } from 'tailwind-variants';

/**
 * 全站按钮的唯一样式来源。此前同一组类名在视图里被抄了 40 余处，
 * 改一次底色要改十几个文件（「多处复用的样式必须封装成组件」）。
 */
export const buttonVariants = tv({
  base: 'inline-flex items-center justify-center gap-1.5 whitespace-nowrap font-medium transition-colors btn-press cursor-pointer disabled:opacity-50 disabled:cursor-not-allowed',
  variants: {
    variant: {
      primary:
        'bg-zinc-900 text-white hover:bg-zinc-800 dark:bg-zinc-100 dark:text-zinc-900 dark:hover:bg-zinc-200',
      outline:
        'border border-zinc-300 text-zinc-700 hover:bg-zinc-50 dark:border-zinc-700 dark:text-zinc-200 dark:hover:bg-zinc-800',
      ghost:
        'text-zinc-600 hover:bg-zinc-100 hover:text-zinc-900 dark:text-zinc-400 dark:hover:bg-zinc-800 dark:hover:text-zinc-100',
      danger: 'bg-rose-600 text-white hover:bg-rose-700',
      'danger-outline':
        'border border-rose-300 text-rose-700 hover:bg-rose-50 dark:border-rose-900 dark:text-rose-400 dark:hover:bg-rose-950/40',
    },
    size: {
      xs: 'rounded-md px-2 py-1 text-xs',
      sm: 'rounded-lg px-2.5 py-1.5 text-xs',
      md: 'rounded-xl px-3.5 py-2 text-xs',
      lg: 'rounded-xl px-4 py-2 text-sm',
    },
  },
  defaultVariants: { variant: 'primary', size: 'md' },
});
/** 状态徽章（已撤销、可见性、题型、标签等）的唯一样式来源。 */
export const badgeVariants = tv({
  base: 'inline-flex items-center gap-1 rounded-full px-2 py-0.5 text-xs font-medium',
  variants: {
    variant: {
      neutral: 'bg-zinc-100 text-zinc-600 dark:bg-zinc-800 dark:text-zinc-400',
      info: 'bg-blue-50 text-blue-700 dark:bg-blue-950/40 dark:text-blue-300',
      success: 'bg-emerald-50 text-emerald-700 dark:bg-emerald-950/40 dark:text-emerald-300',
      warning: 'bg-amber-50 text-amber-700 dark:bg-amber-950/40 dark:text-amber-300',
      danger: 'bg-rose-50 text-rose-700 dark:bg-rose-950/40 dark:text-rose-300',
    },
  },
  defaultVariants: { variant: 'neutral' },
});
