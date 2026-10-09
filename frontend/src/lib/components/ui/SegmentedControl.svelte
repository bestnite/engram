<script lang="ts">
  import { ToggleGroup } from 'bits-ui';
  import { cn } from './utils';

  /**
   * 分段切换：两三个互斥选项（如「文件 / 链接」）。选项少时比下拉更直接——一眼看到全部选项，
   * 一次点击完成切换。底层是 bits-ui 的 ToggleGroup（键盘方向键、aria 状态由库负责）。
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
    /** 每一段的 data-testid 前缀：`<prefix><value>`。 */
    itemTestIdPrefix?: string;
    class?: string;
  }

  let { value, options, onValueChange, ariaLabel, testId, itemTestIdPrefix, class: klass = '' }: Props = $props();

  // single 模式下再次点击已选项会把值清空；分段切换必须始终有一项被选中，所以忽略空值。
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
