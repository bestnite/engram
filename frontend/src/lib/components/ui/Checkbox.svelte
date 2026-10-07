<script lang="ts">
  import { Checkbox as BitsCheckbox } from 'bits-ui';
  import { Check } from '@lucide/svelte';
  import { cn } from './utils';

  /**
   * 全站唯一的复选框（DESIGN.md §8：交互件用成熟组件库）。
   * 原生 <input type="checkbox"> 在深浅两套主题下的系统外观不一致，且三态
   * （indeterminate）需要手写 property，交给 bits-ui 处理。
   */
  interface Props {
    checked?: boolean;
    indeterminate?: boolean;
    disabled?: boolean;
    /** 无可见文字时必须给 label（读屏名称）。 */
    label?: string;
    testId?: string;
    class?: string;
    onCheckedChange?: (checked: boolean) => void;
    /** 阻止点击冒泡到外层可点击容器（如整张卡片）时使用。 */
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
    'flex size-4 shrink-0 cursor-pointer items-center justify-center rounded border border-zinc-300 transition-colors data-[state=checked]:border-blue-600 data-[state=checked]:bg-blue-600 data-[state=indeterminate]:border-blue-600 data-[state=indeterminate]:bg-blue-600 disabled:cursor-not-allowed disabled:opacity-50 dark:border-zinc-600',
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
