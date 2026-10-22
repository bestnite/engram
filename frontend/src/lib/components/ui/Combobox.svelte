<script lang="ts">
  import { Combobox as BitsCombobox } from 'bits-ui';
  import { ChevronDown, Check } from '@lucide/svelte';
  import { cn } from './utils';
  import type { SelectOption } from './options';

  /**
   * The site's only searchable dropdown (same origin as Select: bits-ui's Select state machine + text input filtering).
   *
   * There is only one criterion for choosing it over Select: enough candidates that input filtering is needed (e.g. the full IANA timezone list). When candidates
   * are few and can be scanned at once, use Select - an extra input box is just noise.
   *
   * Filtering is this component's job: bits-ui's Combobox only emits the input text and does not filter the list for the caller. Three
   * details must not be broken: (1) the input box both "shows the current value" and "filters", and when the two are equal it counts as unfiltered, so opening shows
   * the full list instead of only the current item; (2) on close the input resets to the current value's label, otherwise a stray search term is left
   * in the field (looking like the value was changed); (3) clicking the current item again does not clear it (allowDeselect
   * is off) - the value here is required, and clearing would only turn it into an empty string and leave the field blank; "no such value" should be expressed
   * by an explicit entry supplied by the caller (e.g. UTC in the timezone list).
   */
  interface Props {
    value?: string;
    options: SelectOption[];
    /** Placeholder copy for the input when nothing is selected. */
    placeholder?: string;
    disabled?: boolean;
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
    ariaLabel,
    testId,
    id,
    class: klass = '',
    onValueChange,
  }: Props = $props();

  let open = $state(false);
  // Input text: shows the current value's label when closed and the search term when open. bits-ui's inputValue can only be fed one way
  // (not bindable inside the root), so local state is kept here as the single source and synced in the input event.
  let displayText = $state('');

  const selectedLabel = $derived(options.find((option) => option.value === value)?.label ?? '');

  // When open, both typeahead and keyboard navigation rely on this list (bits-ui's items contract).
  const items = $derived(options.map(({ value: v, label, disabled: d }) => ({ value: v, label, disabled: d })));

  const filtered = $derived.by(() => {
    const query = displayText.trim().toLowerCase();
    if (!open || query === '' || displayText === selectedLabel) return options;
    return options.filter((option) => option.label.toLowerCase().includes(query));
  });

  $effect(() => {
    if (!open) displayText = selectedLabel;
  });
</script>

<BitsCombobox.Root
  type="single"
  bind:value
  bind:open
  inputValue={displayText}
  {items}
  {disabled}
  allowDeselect={false}
  onValueChange={(next) => onValueChange?.(next)}
>
  <div class="relative">
    <!-- 点一下字段就展开列表：bits-ui 只在打字或点触发器时开菜单，仅靠 Trigger 会让
         「点开看看有哪些选项」这个最自然的操作落空。 -->
    <BitsCombobox.Input
      {id}
      data-testid={testId}
      aria-label={ariaLabel}
      {placeholder}
      onclick={() => (open = true)}
      oninput={(event) => (displayText = (event.currentTarget as HTMLInputElement).value)}
      class={cn('field-input text-sm w-full pr-9', klass)}
    />
    <BitsCombobox.Trigger
      class="absolute end-1.5 top-1/2 -translate-y-1/2 cursor-pointer rounded-md p-1 text-muted-foreground transition-colors hover:bg-muted"
    >
      <ChevronDown class="size-4 shrink-0 opacity-60" aria-hidden="true" />
    </BitsCombobox.Trigger>
  </div>
  <BitsCombobox.Portal>
    <BitsCombobox.Content
      class="z-50 min-w-[var(--bits-combobox-anchor-width)] overflow-hidden rounded-lg border border-border bg-popover shadow-lg shadow-black/5 data-[state=open]:animate-in data-[state=open]:fade-in-0 data-[state=open]:zoom-in-95 data-[state=closed]:animate-out data-[state=closed]:fade-out-0 data-[state=closed]:zoom-out-95"
      sideOffset={4}
    >
      <BitsCombobox.Viewport class="max-h-72 p-1">
        {#each filtered as option (option.value)}
          <BitsCombobox.Item
            value={option.value}
            label={option.label}
            disabled={option.disabled}
            class="relative flex h-8 cursor-pointer select-none items-center gap-2 rounded-md px-2 text-[13px] text-foreground outline-hidden data-[highlighted]:bg-muted data-[disabled]:cursor-not-allowed data-[disabled]:opacity-50"
          >
            {#snippet children({ selected: isSelected })}
              <Check class={cn('size-3.5 shrink-0', isSelected ? 'opacity-100' : 'opacity-0')} aria-hidden="true" />
              <span class="truncate">{option.label}</span>
            {/snippet}
          </BitsCombobox.Item>
        {/each}
      </BitsCombobox.Viewport>
    </BitsCombobox.Content>
  </BitsCombobox.Portal>
</BitsCombobox.Root>
