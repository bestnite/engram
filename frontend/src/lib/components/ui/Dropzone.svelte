<script lang="ts">
  import { Upload, FileText, X } from '@lucide/svelte';
  import { t, localeStore } from '../../i18n';
  import { cn } from './utils';

  /**
   * File picker area: icon + dashed border + drop highlight, replacing the browser's native file control.
   *
   * The native input[type=file] button text and styling are decided by the browser and system language ("Choose file / No file chosen"),
   * following neither the app language nor the light/dark theme. Here the real input is hidden as sr-only to keep keyboard and screen reader access,
   * and the whole appearance is drawn by this component; data-testid sits on the real input, so tests can still set files on it directly.
   *
   * Two usages: bind:file to get the selected file (used later on form submit); or pass onfile to handle it the moment it is selected (e.g. upload immediately).
   */
  interface Props {
    file?: File | null;
    accept?: string;
    /** One line of explanation below the drop area (supported formats, size limit). */
    hint?: string;
    disabled?: boolean;
    /** data-testid of the real input. */
    testId?: string;
    name?: string;
    /** Handed to the caller immediately on selection; in that case the component keeps no selected file and the drop area resets. */
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
  // dragenter/dragleave fire in pairs between child elements, so a counter decides whether the pointer really left the drop area.
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
    // Dropped files bypass the input's accept filter, so the same check is applied here; anything that does not match is ignored.
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
