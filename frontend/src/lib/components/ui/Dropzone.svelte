<script lang="ts">
  import { Upload, FileText, X } from '@lucide/svelte';
  import { t, localeStore } from '../../i18n';
  import { cn } from './utils';

  /**
   * 文件选择区：图标 + 虚线边框 + 拖入高亮，替代浏览器原生的文件控件。
   *
   * 原生 input[type=file] 的按钮文字与样式由浏览器和系统语言决定（「选择文件 / 未选择任何文件」），
   * 既不跟随应用语言，也不跟随深浅主题。这里把真实的 input 藏成 sr-only 保留键盘与读屏可达性，
   * 外观完全由本组件绘制；data-testid 落在真实 input 上，测试仍可直接给它设置文件。
   *
   * 两种用法：bind:file 拿到选中的文件（表单提交时再用）；或传 onfile 在选中瞬间处理（如立即上传）。
   */
  interface Props {
    file?: File | null;
    accept?: string;
    /** 选区下方的一行说明（支持的格式、大小上限）。 */
    hint?: string;
    disabled?: boolean;
    /** 真实 input 的 data-testid。 */
    testId?: string;
    name?: string;
    /** 选中后立即交给调用方处理；此时组件不保留已选文件，选区回到初始状态。 */
    onfile?: (file: File) => void;
    class?: string;
  }

  let {
    file = $bindable(null),
    accept,
    hint,
    disabled = false,
    testId,
    name,
    onfile,
    class: klass = '',
  }: Props = $props();

  let input = $state<HTMLInputElement | null>(null);
  let dragging = $state(false);
  // dragenter/dragleave 会在子元素之间成对触发，用计数判断指针是否真的离开了选区。
  let dragDepth = 0;

  function accepts(candidate: File): boolean {
    if (!accept) return true;
    const rules = accept.split(',').map((r) => r.trim().toLowerCase()).filter(Boolean);
    const lower = candidate.name.toLowerCase();
    const type = candidate.type.toLowerCase();
    return rules.some((rule) => {
      if (rule.startsWith('.')) return lower.endsWith(rule);
      if (rule.endsWith('/*')) return type.startsWith(rule.slice(0, -1));
      return type === rule;
    });
  }

  function take(candidate: File | null | undefined): void {
    if (!candidate) return;
    if (onfile) {
      onfile(candidate);
      if (input) input.value = '';
      return;
    }
    file = candidate;
  }

  function handleChange(event: Event & { currentTarget: HTMLInputElement }): void {
    take(event.currentTarget.files?.[0]);
  }

  function handleDragEnter(event: DragEvent): void {
    if (disabled) return;
    event.preventDefault();
    dragDepth += 1;
    dragging = true;
  }

  function handleDragOver(event: DragEvent): void {
    if (disabled) return;
    event.preventDefault();
    if (event.dataTransfer) event.dataTransfer.dropEffect = 'copy';
  }

  function handleDragLeave(): void {
    dragDepth = Math.max(0, dragDepth - 1);
    if (dragDepth === 0) dragging = false;
  }

  function handleDrop(event: DragEvent): void {
    if (disabled) return;
    event.preventDefault();
    dragDepth = 0;
    dragging = false;
    const dropped = event.dataTransfer?.files?.[0];
    // 拖入的文件绕过了 input 的 accept 过滤，这里补同一道检查；不符合的直接忽略。
    if (dropped && accepts(dropped)) take(dropped);
  }

  function clear(): void {
    file = null;
    if (input) input.value = '';
  }

  function formatSize(bytes: number): string {
    const units = ['B', 'KB', 'MB', 'GB'];
    let value = bytes;
    let unit = 0;
    while (value >= 1024 && unit < units.length - 1) {
      value /= 1024;
      unit += 1;
    }
    const digits = unit === 0 ? 0 : 1;
    return `${new Intl.NumberFormat($localeStore, { maximumFractionDigits: digits }).format(value)} ${units[unit]}`;
  }
</script>

<div class={cn('space-y-2', klass)}>
  <label
    class={cn(
      'group relative flex cursor-pointer flex-col items-center justify-center gap-2 rounded-lg border border-dashed px-4 py-7 text-center transition-colors duration-150',
      dragging ? 'border-brand bg-brand-soft' : 'border-input bg-surface hover:border-foreground/25 hover:bg-muted/60',
      disabled && 'pointer-events-none opacity-50',
      'has-[input:focus-visible]:outline-2 has-[input:focus-visible]:outline-offset-2 has-[input:focus-visible]:outline-ring'
    )}
    ondragenter={handleDragEnter}
    ondragover={handleDragOver}
    ondragleave={handleDragLeave}
    ondrop={handleDrop}
    data-dragging={dragging || undefined}
  >
    <span
      class={cn(
        'inline-flex size-10 items-center justify-center rounded-full border bg-background text-muted-foreground transition-transform duration-150',
        dragging ? 'scale-110 border-brand/40 text-brand' : 'border-border group-hover:text-foreground'
      )}
      aria-hidden="true"
    >
      <Upload class="size-4.5" />
    </span>
    <span class="text-sm text-foreground">
      {#if dragging}
        {$t('dropzone.release')}
      {:else}
        {$t('dropzone.prompt')}
        <span class="font-medium text-brand underline-offset-2 group-hover:underline">{$t('dropzone.browse')}</span>
      {/if}
    </span>
    {#if hint}
      <span class="text-xs text-muted-foreground">{hint}</span>
    {/if}
    <input
      bind:this={input}
      type="file"
      class="sr-only"
      {accept}
      {name}
      {disabled}
      data-testid={testId}
      onchange={handleChange}
    />
  </label>

  {#if file && !onfile}
    <div
      class="flex items-center gap-3 rounded-lg border border-border px-3 py-2 animate-in fade-in-0 slide-in-from-top-1 duration-150"
      data-testid={testId ? `${testId}-selected` : undefined}
    >
      <FileText class="size-4 shrink-0 text-muted-foreground" aria-hidden="true" />
      <span class="min-w-0 flex-1 truncate text-sm text-foreground">{file.name}</span>
      <span class="shrink-0 text-xs tabular-nums text-muted-foreground">{formatSize(file.size)}</span>
      <button
        type="button"
        onclick={clear}
        class="inline-flex size-7 shrink-0 items-center justify-center rounded-md text-muted-foreground transition-colors hover:bg-muted hover:text-foreground cursor-pointer"
        aria-label={$t('dropzone.remove')}
        title={$t('dropzone.remove')}
      >
        <X class="size-4" aria-hidden="true" />
      </button>
    </div>
  {/if}
</div>
