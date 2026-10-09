<script lang="ts">
  import { tick } from 'svelte';
  import { Pencil } from '@lucide/svelte';
  import { cn } from './ui/utils';

  /**
   * 原地编辑：平时显示文字，点击文字或旁边的铅笔图标变成输入框。
   * Enter 或失焦保存，Esc 取消；多行模式下 Shift+Enter 换行。
   *
   * onSave 返回 false 表示保存失败（调用方负责提示原因），此时停留在编辑态，输入不丢。
   * 不可编辑时（如非属主）只渲染文字，没有任何可点区域。
   */
  interface Props {
    value: string;
    onSave: (next: string) => Promise<boolean>;
    editable?: boolean;
    multiline?: boolean;
    /** 值为空时显示的占位文字（仅可编辑时显示，提示可以添加）。 */
    placeholder?: string;
    /** 输入框与编辑按钮的可读名称。 */
    label: string;
    maxlength?: number;
    /** 显示态文字的样式；编辑态输入框沿用同样的字号与字重，切换时不跳。 */
    class?: string;
    testId?: string;
  }

  let {
    value,
    onSave,
    editable = true,
    multiline = false,
    placeholder = '',
    label,
    maxlength,
    class: klass = '',
    testId,
  }: Props = $props();

  let editing = $state(false);
  let draft = $state('');
  let saving = $state(false);
  let field = $state<HTMLInputElement | HTMLTextAreaElement | null>(null);

  async function start(): Promise<void> {
    if (!editable || editing) return;
    draft = value;
    editing = true;
    await tick();
    field?.focus();
    field?.select();
  }

  async function commit(): Promise<void> {
    if (saving) return;
    if (draft === value) {
      editing = false;
      return;
    }
    saving = true;
    const ok = await onSave(draft);
    saving = false;
    if (ok) editing = false;
    else field?.focus();
  }

  function cancel(): void {
    draft = value;
    editing = false;
  }

  function onKeydown(event: KeyboardEvent): void {
    if (event.key === 'Escape') {
      event.preventDefault();
      cancel();
    } else if (event.key === 'Enter' && !(multiline && event.shiftKey) && !event.isComposing) {
      // isComposing：中文输入法选词时的 Enter 不能当成保存。
      event.preventDefault();
      void commit();
    }
  }

  // 输入框与显示文字共用字号/字重/行高，只加最小的内边距与边框，切换时视觉位置基本不动。
  const fieldClass = $derived(
    cn('-mx-1.5 -my-0.5 w-[calc(100%+0.75rem)] rounded-md border border-ring bg-background px-1.5 py-0.5 outline-none ring-3 ring-ring/15', klass)
  );
</script>

{#if editing}
  {#if multiline}
    <textarea
      bind:this={field}
      bind:value={draft}
      rows="2"
      {maxlength}
      aria-label={label}
      disabled={saving}
      data-testid={testId ? `${testId}-input` : undefined}
      class={cn(fieldClass, 'block resize-none')}
      onkeydown={onKeydown}
      onblur={commit}
    ></textarea>
  {:else}
    <input
      bind:this={field}
      bind:value={draft}
      {maxlength}
      aria-label={label}
      disabled={saving}
      data-testid={testId ? `${testId}-input` : undefined}
      class={cn(fieldClass, 'block')}
      onkeydown={onKeydown}
      onblur={commit}
    />
  {/if}
{:else if editable}
  <span class="group/inline flex min-w-0 items-start gap-1.5">
    <!-- 文字本身也可点：编辑入口不该只有一个小图标。 -->
    <span
      class={cn('min-w-0 cursor-text break-words rounded-sm', !value && 'text-muted-foreground/70', klass)}
      onclick={start}
      role="presentation"
      data-testid={testId}
    >{value || placeholder}</span>
    <button
      type="button"
      onclick={start}
      class="mt-0.5 inline-flex size-6 shrink-0 items-center justify-center rounded-md text-muted-foreground opacity-0 transition hover:bg-muted hover:text-foreground focus-visible:opacity-100 group-hover/inline:opacity-100 max-md:opacity-100 cursor-pointer"
      aria-label={label}
      title={label}
      data-testid={testId ? `${testId}-edit` : undefined}
    >
      <Pencil class="size-3.5" aria-hidden="true" />
    </button>
  </span>
{:else if value}
  <span class={klass} data-testid={testId}>{value}</span>
{/if}
