import { tv } from 'tailwind-variants';

/**
 * The single source of button styling site-wide. Previously the same class set was copied into views in 40-odd places,
 * changing a background color meant editing a dozen files ("styles reused in several places must be wrapped in a component").
 *
 * All colors come from app.css's semantic tokens; light/dark is switched by the tokens and no dark: variants are written here.
 * Sizes are defined as fixed heights: buttons, inputs and dropdowns in the same row align naturally as long as they pick the same size.
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

/** The single source of styling for status badges (revoked, card kind, tags, etc.). */
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
 * Styling of the parts of a dropdown menu (bits-ui DropdownMenu). The account menu and list-row "more" menus both use this set,
 * so the overlay's background, shadow and enter/exit animation are consistent site-wide.
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

/**
 * Unified list styling (decks, notes, users...): a bordered container with only dividers between rows,
 * no longer one card per item. The deck lists on the home page and the deck page serve different purposes with different columns, but row height, hover,
 * selection and clickable area all come from this set, so they look like the same kind of thing.
 */
export const listClasses = {
  root: 'overflow-hidden rounded-lg border border-border',
  header: 'flex h-9 items-center gap-3 border-b border-border bg-surface px-4 text-xs font-medium text-muted-foreground',
  row: 'group relative flex items-center gap-3 border-b border-border px-4 py-3 transition-colors last:border-b-0 hover:bg-muted/50 has-[[data-state=checked]]:bg-brand-soft/60',
  // Whole-row clickable: the title link's pseudo-element covers the row; other controls inside the row layer above it via rowAction, so clicks do not interfere.
  rowLink:
    "min-w-0 truncate font-medium text-foreground outline-none after:absolute after:inset-0 after:content-[''] focus-visible:after:rounded-md focus-visible:after:outline-2 focus-visible:after:-outline-offset-2 focus-visible:after:outline-ring",
  rowAction: 'relative z-10',
} as const;

/** Button inside SelectionBar: a light-text button on the dark bar. */
export const selectionBarButton =
  'inline-flex h-8 shrink-0 items-center gap-1.5 whitespace-nowrap rounded-md px-2.5 text-[13px] font-medium text-primary-foreground transition-colors hover:bg-primary-foreground/10 disabled:opacity-50 cursor-pointer [&_svg]:size-4';
