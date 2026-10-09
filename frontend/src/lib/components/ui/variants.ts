import { tv } from 'tailwind-variants';

/**
 * 全站按钮的唯一样式来源。此前同一组类名在视图里被抄了 40 余处，
 * 改一次底色要改十几个文件（「多处复用的样式必须封装成组件」）。
 *
 * 颜色全部取 app.css 的语义令牌，深浅主题由令牌切换，这里不写 dark: 变体。
 * 尺寸按固定高度定义：同一行里的按钮、输入框、下拉只要选同一档就天然对齐。
 */
export const buttonVariants = tv({
  base: 'inline-flex items-center justify-center gap-1.5 whitespace-nowrap rounded-md font-medium transition-colors btn-press cursor-pointer disabled:opacity-50 disabled:cursor-not-allowed [&_svg]:shrink-0',
  variants: {
    variant: {
      primary: 'bg-primary text-primary-foreground hover:bg-primary/88',
      outline: 'border border-input bg-background text-foreground hover:bg-muted',
      ghost: 'text-muted-foreground hover:bg-muted hover:text-foreground',
      danger: 'bg-destructive text-white hover:bg-destructive/90',
      'danger-outline': 'border border-destructive/40 text-destructive-foreground hover:bg-destructive-soft',
    },
    size: {
      xs: 'h-7 px-2 text-xs',
      sm: 'h-8 px-2.5 text-xs',
      md: 'h-8.5 px-3 text-[13px]',
      lg: 'h-9 px-4 text-sm',
      icon: 'size-8 p-0',
    },
  },
  defaultVariants: { variant: 'primary', size: 'md' },
});

/** 状态徽章（已撤销、题型、标签等）的唯一样式来源。 */
export const badgeVariants = tv({
  base: 'inline-flex items-center gap-1 rounded-full px-2 py-0.5 text-xs font-medium',
  variants: {
    variant: {
      neutral: 'bg-muted text-muted-foreground',
      info: 'bg-brand-soft text-brand',
      success: 'bg-emerald-50 text-emerald-700 dark:bg-emerald-950/40 dark:text-emerald-300',
      warning: 'bg-amber-50 text-amber-700 dark:bg-amber-950/40 dark:text-amber-300',
      danger: 'bg-destructive-soft text-destructive-foreground',
    },
  },
  defaultVariants: { variant: 'neutral' },
});

/**
 * 下拉菜单（bits-ui DropdownMenu）各部件的样式。账户菜单、列表行的「更多」菜单都用这一套，
 * 浮层的底色、阴影与进出场动画因此全站一致。
 */
export const menuClasses = {
  content:
    'z-50 min-w-44 rounded-lg border border-border bg-popover p-1 text-foreground shadow-lg shadow-black/8 outline-hidden ' +
    'data-[state=open]:animate-in data-[state=open]:fade-in-0 data-[state=open]:zoom-in-95 ' +
    'data-[state=closed]:animate-out data-[state=closed]:fade-out-0 data-[state=closed]:zoom-out-95 ' +
    'data-[side=bottom]:slide-in-from-top-1 data-[side=top]:slide-in-from-bottom-1 data-[side=right]:slide-in-from-left-1',
  item:
    'relative flex h-8 cursor-pointer select-none items-center gap-2 rounded-md px-2 text-[13px] outline-hidden ' +
    'data-[highlighted]:bg-muted data-[disabled]:cursor-not-allowed data-[disabled]:opacity-50 [&_svg]:size-4 [&_svg]:shrink-0 [&_svg]:text-muted-foreground',
  destructiveItem:
    'relative flex h-8 cursor-pointer select-none items-center gap-2 rounded-md px-2 text-[13px] text-destructive-foreground outline-hidden ' +
    'data-[highlighted]:bg-destructive-soft data-[disabled]:cursor-not-allowed data-[disabled]:opacity-50 [&_svg]:size-4 [&_svg]:shrink-0',
  label: 'px-2 py-1.5 text-xs text-muted-foreground',
  separator: '-mx-1 my-1 h-px bg-border',
} as const;
