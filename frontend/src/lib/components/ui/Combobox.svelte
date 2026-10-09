<script lang="ts">
  import { Combobox as BitsCombobox } from 'bits-ui';
  import { ChevronDown, Check } from '@lucide/svelte';
  import { cn } from './utils';
  import type { SelectOption } from './options';

  /**
   * 全站唯一的可搜索下拉（与 Select 同源：bits-ui 的 Select 状态机 + 文本输入筛选）。
   *
   * 选它还是 Select 的判据只有一条：候选项多到需要输入筛选（如 IANA 时区全量）。候选项
   * 少、看一遍就能选完的场景仍用 Select——多一个输入框只是噪音。
   *
   * 筛选由本组件承担：bits-ui 的 Combobox 只把输入文本交出来，不替调用方筛列表。三个
   * 细节别改坏：① 输入框同时承担「显示当前值」与「筛选」，两者相等时视为未筛选，打开就
   * 看到全量列表，而不是只剩当前那一项；② 关闭时把输入框复位成当前值的标签，否则会在
   * 字段里留下一截搜索词（看起来像值被改掉了）；③ 再点一次当前项不清空（allowDeselect
   * 关掉）——这里选的是必填值，清空只会把值变成空串、字段空白，「不设这个值」应当由
   * 调用方提供的显式条目表达（如时区列表里的 UTC）。
   */
  interface Props {
    value?: string;
    options: SelectOption[];
    /** 未选中时输入框的占位文案。 */
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
  // 输入框文本：关闭时显示当前值的标签，展开时是搜索词。bits-ui 的 inputValue 只能单向喂
  // （根内部不可 bind），所以这里保持本地状态做单一来源，输入事件里同步进来。
  let displayText = $state('');

  const selectedLabel = $derived(options.find((option) => option.value === value)?.label ?? '');

  // 打开时 typeahead 与键盘导航都靠这份列表（bits-ui 的 items 契约）。
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
