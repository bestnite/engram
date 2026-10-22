<script lang="ts">
  import { ToggleGroup } from 'bits-ui';
  import { cn } from './utils';

  /**
   * Segmented switch: two or three mutually exclusive options (e.g. "file / link"). With few options it is more direct than a dropdown - all options are visible at a glance,
   * and one click completes the switch. The base is bits-ui's ToggleGroup (keyboard arrows and aria state are handled by the library).
   */
  interface Option {
    value: string;
    label: string;
  }

  interface Props {
    value: string;
    options: Option[];
    onValueChange?: (value: string) => void;
    ariaLabel?: string;
    testId?: string;
    /** data-testid prefix for each segment: `<prefix><value>`. */
    itemTestIdPrefix?: string;
    class?: string;
  }

  let { value, options, onValueChange, ariaLabel, testId, itemTestIdPrefix, class: klass = '' }: Props = $props();

  // In single mode clicking the selected item again clears the value; a segmented switch must always have one selected, so empty values are ignored.
  function handleChange(next: string) {
    if (next && next !== value) onValueChange?.(next);
  }
</script>

<ToggleGroup.Root
  type="single"
  {value}
  onValueChange={handleChange}
  aria-label={ariaLabel}
  data-testid={testId}
  class={cn('inline-flex h-8.5 items-center gap-0.5 rounded-md bg-muted p-0.5', klass)}
>
  {#each options as option (option.value)}
    <ToggleGroup.Item
      value={option.value}
      data-testid={itemTestIdPrefix ? `${itemTestIdPrefix}${option.value}` : undefined}
      class="inline-flex h-full items-center justify-center rounded-[5px] px-3 text-[13px] font-medium text-muted-foreground transition-all hover:text-foreground cursor-pointer data-[state=on]:bg-background data-[state=on]:text-foreground data-[state=on]:shadow-sm"
    >
      {option.label}
    </ToggleGroup.Item>
  {/each}
</ToggleGroup.Root>
