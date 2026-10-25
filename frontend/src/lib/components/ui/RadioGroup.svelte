<script lang="ts">
  import { RadioGroup as BitsRadioGroup } from 'bits-ui';
  import { cn } from './utils';

  export interface RadioOption {
    value: string;
    label: string;
    /**
     * Server-sanitized HTML rendered instead of the plain label. It must never receive raw user text: only HTML that already went
     * through the server's sanitizing Markdown/TeX pipeline may be injected here, because this is an HTML sink.
     */
    html?: string;
    disabled?: boolean;
  }

  /**
   * The site's only radio group (interactive widgets use a mature component library).
   *
   * The base is bits-ui's RadioGroup: arrow-key switching, roving tabindex, aria-radiogroup wiring
   * and the form semantics of the hidden input all come from the library; previously each radio group hand-wrote native radios + labels.
   */
  interface Props {
    value?: string;
    options: RadioOption[];
    name?: string;
    disabled?: boolean;
    ariaLabel?: string;
    testId?: string;
    /** All options share one testid (the review page's options are exactly this shape). */
    itemTestId?: string;
    /** Use a prefix when options need to be distinguished: testid = prefix + option.value. */
    itemTestIdPrefix?: string;
    itemClass?: string;
    class?: string;
    onValueChange?: (value: string) => void;
  }

  let {
    value = $bindable(''),
    options,
    name,
    disabled = false,
    ariaLabel,
    testId,
    itemTestId,
    itemTestIdPrefix,
    itemClass = '',
    class: klass = '',
    onValueChange,
  }: Props = $props();
</script>

<BitsRadioGroup.Root
  bind:value
  {name}
  {disabled}
  aria-label={ariaLabel}
  data-testid={testId}
  onValueChange={(next) => onValueChange?.(next)}
  class={cn('space-y-2', klass)}
>
  {#each options as option (option.value)}
    <BitsRadioGroup.Item
      value={option.value}
      disabled={option.disabled}
      data-testid={itemTestIdPrefix ? `${itemTestIdPrefix}${option.value}` : itemTestId}
      class={cn(
        'flex cursor-pointer items-center gap-3 rounded-xl border border-zinc-300 px-4 py-2.5 text-sm text-zinc-700 transition-colors hover:border-zinc-400 data-[state=checked]:border-blue-500 data-[state=checked]:bg-blue-50 data-[state=checked]:font-medium data-[state=checked]:text-blue-700 dark:border-zinc-700 dark:text-zinc-300 dark:hover:border-zinc-500 dark:data-[state=checked]:border-blue-600 dark:data-[state=checked]:bg-blue-950/40 dark:data-[state=checked]:text-blue-300',
        itemClass
      )}
    >
      {#if option.html}{@html option.html}{:else}{option.label}{/if}
    </BitsRadioGroup.Item>
  {/each}
</BitsRadioGroup.Root>
