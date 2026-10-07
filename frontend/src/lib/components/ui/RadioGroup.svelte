<script lang="ts">
  import { RadioGroup as BitsRadioGroup } from 'bits-ui';
  import { cn } from './utils';

  export interface RadioOption {
    value: string;
    label: string;
    disabled?: boolean;
  }

  /**
   * 全站唯一的单选组（DESIGN.md §8：交互件用成熟组件库）。
   *
   * 底层是 bits-ui 的 RadioGroup：方向键切换、roving tabindex、aria-radiogroup 关联
   * 与隐藏 input 的表单语义都由库提供；此前每个单选组各自手写原生 radio + label。
   */
  interface Props {
    value?: string;
    options: RadioOption[];
    name?: string;
    disabled?: boolean;
    ariaLabel?: string;
    testId?: string;
    /** 所有选项共用同一个 testid（复习页的选项就是这一形态）。 */
    itemTestId?: string;
    /** 需要逐项区分时用前缀：testid = 前缀 + option.value。 */
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
      {option.label}
    </BitsRadioGroup.Item>
  {/each}
</BitsRadioGroup.Root>
