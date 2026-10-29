<script lang="ts">
  import { tick } from 'svelte';
  import { Pencil } from '@lucide/svelte';
  import { cn } from './ui/utils';

  /**
   * Inline editing: normally shows text; clicking the text or the pencil icon next to it turns it into an input.
   * Enter or blur saves, Esc cancels; in multiline mode Shift+Enter inserts a newline.
   *
   * onSave returning false means the save failed (the caller is responsible for reporting why); the component stays in edit mode and the input is not lost.
   * When not editable (e.g. not the owner) it renders only the text, with no clickable area.
   */
  interface Props {
    value: string;
    onSave: (next: string) => Promise<boolean>;
    editable?: boolean;
    multiline?: boolean;
    /** Placeholder text shown when the value is empty (only when editable, hinting that something can be added). */
    placeholder?: string;
    /** Accessible name for the input and the edit button. */
    label: string;
    maxlength?: number;
    /** Style of the display-state text; the edit-state input keeps the same font size and weight so nothing jumps when switching. */
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
      // isComposing: Enter while an IME is composing must not be treated as a save.
      event.preventDefault();
      void commit();
    }
  }

  // The input and the display text share font size/weight/line height; only minimal padding and border are added, so the visual position barely moves when switching.
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
    <!-- The text itself is clickable too: a small icon should not be the only way into editing. -->
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
