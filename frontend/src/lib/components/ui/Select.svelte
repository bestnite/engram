<script lang="ts">
  import { Select as BitsSelect } from 'bits-ui';
  import { ChevronDown, Check } from '@lucide/svelte';
  import { cn } from './utils';

  export interface SelectOption {
    value: string;
    label: string;
    disabled?: boolean;
  }

  /**
   * 全站唯一的单选下拉（DESIGN.md §8：交互件用成熟组件库）。
   *
   * 底层是 bits-ui 的 Select（无障碍、键盘导航、typeahead 由库承担，不再自写），
   * 这里只做两件事：① 把项目视觉（圆角/边框/深色/焦点环）收敛到一处；
   * ② 把调用面收成 `options` 数组 + `bind:value`，替换原生 select 元素 时逐处改动最小。
   */
  interface Props {
    value?: string;
    options: SelectOption[];
    /** 未选中时触发器里显示的文案。 */
    placeholder?: string;
    disabled?: boolean;
    /** 触发器尺寸：sm 用于表格行内，md 用于表单。 */
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
    size = 'md',
    ariaLabel,
    testId,
    id,
    class: klass = '',
    onValueChange,
  }: Props = $props();

  const selected = $derived(options.find((option) => option.value === value));

  // 打开时 types–ahead 与表单自动填充都靠这份列表（bits-ui 的 items 契约）。
  const items = $derived(options.map(({ value: v, label, disabled: d }) => ({ value: v, label, disabled: d })));

  const triggerSize = { sm: 'py-1 text-xs', md: 'py-2 text-xs' } as const;
</script>

<BitsSelect.Root type="single" bind:value {items} {disabled} onValueChange={(next) => onValueChange?.(next)}>
  <BitsSelect.Trigger
    {id}
    data-testid={testId}
    aria-label={ariaLabel}
    class={cn(
      'inline-flex w-full cursor-pointer items-center justify-between gap-2 rounded-xl border border-zinc-300 bg-white px-3 text-zinc-900 transition-colors hover:bg-zinc-50 disabled:cursor-not-allowed disabled:opacity-50 dark:border-zinc-700 dark:bg-zinc-900 dark:text-zinc-100 dark:hover:bg-zinc-800',
      triggerSize[size],
      klass
    )}
  >
    <span class={cn('truncate text-left', !selected && 'text-zinc-400 dark:text-zinc-500')}>
      {selected?.label ?? placeholder}
    </span>
    <ChevronDown class="size-4 shrink-0 opacity-60" aria-hidden="true" />
  </BitsSelect.Trigger>
  <BitsSelect.Portal>
    <BitsSelect.Content
      class="z-50 min-w-[var(--bits-select-anchor-width)] overflow-hidden rounded-xl border border-zinc-200 bg-white shadow-lg dark:border-zinc-700 dark:bg-zinc-900"
      sideOffset={4}
    >
      <BitsSelect.Viewport class="max-h-72 p-1">
        {#each options as option (option.value)}
          <BitsSelect.Item
            value={option.value}
            label={option.label}
            disabled={option.disabled}
            class="relative flex cursor-pointer select-none items-center gap-2 rounded-lg px-2.5 py-2 text-xs text-zinc-700 outline-hidden data-[highlighted]:bg-zinc-100 data-[highlighted]:text-zinc-900 data-[disabled]:cursor-not-allowed data-[disabled]:opacity-50 dark:text-zinc-300 dark:data-[highlighted]:bg-zinc-800 dark:data-[highlighted]:text-zinc-100"
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
