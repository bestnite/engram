<script lang="ts">
  import { Select as BitsSelect } from 'bits-ui';
  import { ChevronDown, Check } from '@lucide/svelte';
  import { cn } from './utils';
  import type { SelectOption } from './options';

  /**
   * 全站唯一的单选下拉（交互件用成熟组件库）。
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
    /**
     * 再点一次当前项是否清空选择。库的默认是清空，但「清空」在单选控件上只会把值变成
     * 空串、触发器退回留白，而调用方大多把它当必填（卡组、预设、语言）：那是一次误操作
     * 而不是一次选择。要求必有值的调用点显式传 false。
     */
    allowDeselect?: boolean;
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
    allowDeselect = true,
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
