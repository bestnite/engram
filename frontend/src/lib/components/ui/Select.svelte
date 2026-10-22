<script lang="ts">
  import { Select as BitsSelect } from 'bits-ui';
  import { ChevronDown, Check } from '@lucide/svelte';
  import { cn } from './utils';
  import type { SelectOption } from './options';

  /**
   * The site's only single-select dropdown (interactive widgets use a mature component library).
   *
   * The base is bits-ui's Select (accessibility, keyboard navigation and typeahead are the library's job, no longer hand-written),
   * and this only does two things: (1) converge the project's visuals (radius/border/dark mode/focus ring) into one place;
   * (2) reduce the call surface to an `options` array + `bind:value`, so replacing native select elements changes each site minimally.
   */
  interface Props {
    value?: string;
    options: SelectOption[];
    /** Copy shown in the trigger when nothing is selected. */
    placeholder?: string;
    disabled?: boolean;
    /**
     * Whether clicking the current item again clears the selection. The library's default is to clear, but on a single-select control "clear" only turns the value into
     * an empty string and leaves the trigger blank, while most callers treat it as required (deck, preset, language): that is a misclick,
     * not a selection. Call sites that require a value pass false explicitly.
     */
    allowDeselect?: boolean;
    /** Trigger size: sm for inside table rows, md for forms. */
    size?: 'sm' | 'md';
    ariaLabel?: string;
    testId?: string;
    id?: string;
    class?: string;
    onValueChange?: (value: string) => void;
  }

  let {
    value = $bindable(''),
    options,
    placeholder = '',
    disabled = false,
    allowDeselect = true,
    size = 'md',
    ariaLabel,
    testId,
    id,
    class: klass = '',
    onValueChange,
  }: Props = $props();

  const selected = $derived(options.find((option) => option.value === value));

  // When open, typeahead and form autofill both rely on this list (bits-ui's items contract).
  const items = $derived(options.map(({ value: v, label, disabled: d }) => ({ value: v, label, disabled: d })));

  const triggerSize = { sm: 'h-8 text-xs', md: 'h-9 text-sm' } as const;
</script>

<BitsSelect.Root type="single" bind:value {items} {disabled} {allowDeselect} onValueChange={(next) => onValueChange?.(next)}>
  <BitsSelect.Trigger
    {id}
    data-testid={testId}
    aria-label={ariaLabel}
    class={cn(
      'inline-flex w-full cursor-pointer items-center justify-between gap-2 rounded-md border border-input bg-background px-3 text-foreground transition-colors hover:bg-muted/50 disabled:cursor-not-allowed disabled:opacity-50 data-[state=open]:border-ring',
      triggerSize[size],
      klass
    )}
  >
    <span class={cn('truncate text-left', !selected && 'text-muted-foreground/70')}>
      {selected?.label ?? placeholder}
    </span>
    <ChevronDown class="size-4 shrink-0 opacity-60" aria-hidden="true" />
  </BitsSelect.Trigger>
  <BitsSelect.Portal>
    <BitsSelect.Content
      class="z-50 min-w-[var(--bits-select-anchor-width)] overflow-hidden rounded-lg border border-border bg-popover shadow-lg shadow-black/5 data-[state=open]:animate-in data-[state=open]:fade-in-0 data-[state=open]:zoom-in-95 data-[state=closed]:animate-out data-[state=closed]:fade-out-0 data-[state=closed]:zoom-out-95"
      sideOffset={4}
    >
      <BitsSelect.Viewport class="max-h-72 p-1">
        {#each options as option (option.value)}
          <BitsSelect.Item
            value={option.value}
            label={option.label}
            disabled={option.disabled}
            class="relative flex h-8 cursor-pointer select-none items-center gap-2 rounded-md px-2 text-[13px] text-foreground outline-hidden data-[highlighted]:bg-muted data-[disabled]:cursor-not-allowed data-[disabled]:opacity-50"
          >
            {#snippet children({ selected: isSelected })}
              <Check class={cn('size-3.5 shrink-0', isSelected ? 'opacity-100' : 'opacity-0')} aria-hidden="true" />
              <span class="truncate">{option.label}</span>
            {/snippet}
          </BitsSelect.Item>
        {/each}
      </BitsSelect.Viewport>
    </BitsSelect.Content>
  </BitsSelect.Portal>
</BitsSelect.Root>
