<script lang="ts">
  import { Checkbox as BitsCheckbox } from 'bits-ui';
  import { Check } from '@lucide/svelte';
  import { cn } from './utils';

  /**
   * The site's only checkbox (interactive widgets use a mature component library).
   * The native <input type="checkbox"> has inconsistent system appearance across the light and dark themes, and its tri-state
   * (indeterminate) state requires hand-writing the property, so it is left to bits-ui.
   */
  interface Props {
    checked?: boolean;
    indeterminate?: boolean;
    disabled?: boolean;
    /** Must be given a label when there is no visible text (screen reader name). */
    label?: string;
    testId?: string;
    class?: string;
    onCheckedChange?: (checked: boolean) => void;
    /** Use to stop a click from bubbling to a clickable outer container (e.g. a whole card). */
    onclick?: (event: MouseEvent) => void;
  }

  let {
    checked = $bindable(false),
    indeterminate = false,
    disabled = false,
    label,
    testId,
    class: klass = '',
    onCheckedChange,
    onclick,
  }: Props = $props();
</script>

<BitsCheckbox.Root
  bind:checked
  {indeterminate}
  {disabled}
  aria-label={label}
  data-testid={testId}
  onCheckedChange={(next) => onCheckedChange?.(next)}
  {onclick}
  class={cn(
    'flex size-4 shrink-0 cursor-pointer items-center justify-center rounded-[4px] border border-input bg-background transition-colors data-[state=checked]:border-brand data-[state=checked]:bg-brand data-[state=indeterminate]:border-brand data-[state=indeterminate]:bg-brand disabled:cursor-not-allowed disabled:opacity-50',
    klass
  )}
>
  {#snippet children({ checked: isChecked, indeterminate: isIndeterminate })}
    {#if isChecked}
      <Check class="size-3 text-white" strokeWidth={3} aria-hidden="true" />
    {:else if isIndeterminate}
      <span class="h-0.5 w-2 rounded-full bg-white" aria-hidden="true"></span>
    {/if}
  {/snippet}
</BitsCheckbox.Root>
