<script lang="ts">
  /**
   * RichTextField edits one note field inside the sandboxed editing frame.
   *
   * The field's HTML never enters the parent DOM: the frame receives it with `load`, the toolbar in
   * this component sends `command`, and the frame posts the new HTML back with `change`. That is
   * the same isolation as the card frame — the field may contain markup a note author wrote, and
   * only the sandbox may render it.
   *
   * The toolbar acts on the frame's own selection. Commands therefore carry only their argument; the
   * frame keeps the last caret it saw, so a click that moves focus into the toolbar does not lose
   * the insertion point.
   */
  import { appVersion } from '../version';
  import { t } from '../i18n';
  import { uploadErrorKeyFor } from '../media-upload';
  import { toast } from './ui/toast';
  import Dialog from './ui/Dialog.svelte';
  import Button from './ui/Button.svelte';
  import Checkbox from './ui/Checkbox.svelte';
  import type { MediaReference } from '../../editframe/runtime';
  import {
    TextAlignCenter as AlignCenter,
    TextAlignJustify as AlignJustify,
    TextAlignEnd as AlignRight,
    TextAlignStart as AlignLeft,
    Bold,
    ChevronDown,
    ChevronUp,
    CodeXml,
    Highlighter,
    ImagePlus,
    Italic,
    List,
    ListIndentDecrease,
    ListIndentIncrease,
    ListOrdered,
    Music,
    Palette,
    RemoveFormatting,
    Sigma,
    SquareSigma,
    Subscript,
    Superscript,
    Underline,
  } from '@lucide/svelte';

  /** The per-field presentation options a note type's field entry can carry. */
  export interface RichFieldOptions {
    /** Keep the value when the host starts a new note. */
    sticky?: boolean;
    /** Start folded; the writer unfolds the field to edit it. */
    collapsed?: boolean;
    /** The field holds plain text: a textarea instead of rich content, and no formatting toolbar. */
    plainText?: boolean;
    /** Right-to-left writing direction. */
    rtl?: boolean;
    /** CSS font family applied to the editing surface. */
    font?: string;
    /** Font size in px; 0 or absent means the default. */
    size?: number;
    /** Shown in the field while it is empty. */
    description?: string;
  }

  interface Props {
    value: string;
    /** The field's visible name, used to name each action for assistive technology. */
    label: string;
    testId: string;
    onvalue: (value: string) => void;
    /** Presentation options of this field; absent means the defaults (rich, expanded, left to right). */
    options?: RichFieldOptions;
    /** Uploads one file and returns its URL, deck media name and detected mime type. */
    upload?: (file: File) => Promise<MediaReference>;
  }

  let { value, label, testId, onvalue, options = {}, upload }: Props = $props();

  // The version segment only busts the iframe cache; a build without an injected version uses a fixed placeholder.
  const src = $derived(`/edit-frame/${encodeURIComponent(appVersion() || 'dev')}`);

  let iframe = $state<HTMLIFrameElement | null>(null);
  let ready = $state(false);
  let height = $state(0);
  let sourceOpen = $state(false);
  // svelte-ignore state_referenced_locally
  let expanded = $state(!(options.collapsed ?? false));
  let fileInput = $state<HTMLInputElement | null>(null);
  let audioInput = $state<HTMLInputElement | null>(null);
  let audioOpen = $state(false);
  let audioFile = $state<File | null>(null);
  let audioStart = $state('');
  let audioEnd = $state('');
  let audioRate = $state('');
  let audioLoop = $state(false);
  let audioBusy = $state(false);
  let disposed = false;

  // The value the frame last reported. A `value` prop equal to it came from this field, so echoing it
  // back with `load` would reset the caret on every keystroke. `null` means the frame has not reported
  // anything yet, so the first load always happens even for an empty field.
  let lastEcho: string | null = null;
  let loadedSignature = '';

  const plainText = $derived(options.plainText ?? false);
  const rtl = $derived(options.rtl ?? false);
  const font = $derived(options.font ?? '');
  const size = $derived(options.size ?? 0);
  const description = $derived(options.description ?? '');

  const toolbarButton =
    'inline-flex size-7 shrink-0 cursor-pointer items-center justify-center rounded-md text-muted-foreground transition-colors hover:bg-muted hover:text-foreground focus-visible:outline-2 focus-visible:outline-offset-2 focus-visible:outline-ring';

  const separator = 'h-4 w-px shrink-0 bg-input @max-sm:hidden';

  /** One toolbar action: the command name the frame understands and its accessible label key. */
  interface Action {
    id: string;
    name: string;
    Icon: typeof Bold;
    label: string;
    shortcut?: string;
    extra?: Record<string, unknown>;
  }

  const inlineActions: Action[] = [
    { id: 'bold', name: 'bold', Icon: Bold, label: 'editor.bold', shortcut: 'Control+B Meta+B' },
    { id: 'italic', name: 'italic', Icon: Italic, label: 'editor.italic', shortcut: 'Control+I Meta+I' },
    { id: 'underline', name: 'underline', Icon: Underline, label: 'editor.underline', shortcut: 'Control+U Meta+U' },
    { id: 'superscript', name: 'superscript', Icon: Superscript, label: 'editor.superscript' },
    { id: 'subscript', name: 'subscript', Icon: Subscript, label: 'editor.subscript' },
    { id: 'clear', name: 'clearFormatting', Icon: RemoveFormatting, label: 'editor.clear_formatting' },
  ];

  const listActions: Action[] = [
    { id: 'bullet', name: 'unorderedList', Icon: List, label: 'editor.bullet_list' },
    { id: 'ordered', name: 'orderedList', Icon: ListOrdered, label: 'editor.ordered_list' },
  ];

  const alignActions: Action[] = [
    { id: 'align_left', name: 'align', Icon: AlignLeft, label: 'editor.align_left', extra: { value: 'left' } },
    { id: 'align_center', name: 'align', Icon: AlignCenter, label: 'editor.align_center', extra: { value: 'center' } },
    { id: 'align_right', name: 'align', Icon: AlignRight, label: 'editor.align_right', extra: { value: 'right' } },
    { id: 'align_justify', name: 'align', Icon: AlignJustify, label: 'editor.align_justify', extra: { value: 'justify' } },
  ];

  const indentActions: Action[] = [
    { id: 'indent', name: 'indent', Icon: ListIndentIncrease, label: 'editor.indent', extra: { delta: 1 } },
    { id: 'outdent', name: 'indent', Icon: ListIndentDecrease, label: 'editor.outdent', extra: { delta: -1 } },
  ];

  const insertActions: Action[] = [
    { id: 'mathjax_inline', name: 'mathjaxInline', Icon: Sigma, label: 'editor.mathjax_inline' },
    { id: 'mathjax_block', name: 'mathjaxBlock', Icon: SquareSigma, label: 'editor.mathjax_block' },
    { id: 'latex', name: 'latex', Icon: CodeXml, label: 'editor.latex' },
    { id: 'cloze', name: 'cloze', Icon: ChevronUp, label: 'editor.cloze_next', shortcut: 'Control+Shift+C Meta+Shift+C' },
    { id: 'cloze_same', name: 'clozeSame', Icon: ChevronDown, label: 'editor.cloze_same', shortcut: 'Control+Alt+Shift+C Meta+Alt+Shift+C' },
  ];

  function send(message: Record<string, unknown>): void {
    iframe?.contentWindow?.postMessage(message, '*');
  }

  function sendTheme(): void {
    if (!ready) return;
    send({ type: 'theme', dark: document.documentElement.classList.contains('dark') });
  }

  function sendLoad(html: string): void {
    send({
      type: 'load',
      html,
      plainText,
      rtl,
      font,
      size,
      description,
      dark: document.documentElement.classList.contains('dark'),
      source: sourceOpen,
    });
    loadedSignature = signature(html);
  }

  function signature(html: string): string {
    return JSON.stringify({ html, plainText, rtl, font, size, description });
  }

  function command(name: string, extra: Record<string, unknown> = {}): void {
    if (!ready) return;
    send({ type: 'command', name, ...extra });
  }

  function toggleSource(): void {
    if (plainText) return;
    sourceOpen = !sourceOpen;
    if (ready) send({ type: 'source', open: sourceOpen });
    if (!ready) sendLoad(value);
  }

  function applyColor(property: 'textColor' | 'highlight', event: Event): void {
    const input = event.currentTarget as HTMLInputElement;
    command(property, { color: input.value });
  }

  async function uploadFile(file: File): Promise<void> {
    const uploader = upload;
    if (!uploader) return;
    try {
      const media = await uploader(file);
      if (disposed) return;
      command('insertMedia', { media });
    } catch (err) {
      if (disposed) return;
      toast.error($t(uploadErrorKeyFor(err)));
    }
  }

  function pickFile(): void {
    fileInput?.click();
  }

  function onFileChosen(event: Event): void {
    const input = event.currentTarget as HTMLInputElement;
    const file = input.files?.[0];
    input.value = '';
    if (file) void uploadFile(file);
  }

  /** A non-negative decimal, or empty for "not set". */
  function segmentNumber(raw: string): boolean {
    const value = raw.trim();
    return value === '' || /^\d+(\.\d+)?$/.test(value);
  }

  const audioNumberInvalid = $derived(!segmentNumber(audioStart) || !segmentNumber(audioEnd) || !segmentNumber(audioRate));
  const audioRangeInvalid = $derived(
    audioStart.trim() !== '' && audioEnd.trim() !== '' && Number(audioEnd) <= Number(audioStart),
  );
  const audioCanInsert = $derived(audioFile !== null && !audioBusy && !audioNumberInvalid && !audioRangeInvalid);

  function openAudio(): void {
    audioFile = null;
    audioStart = '';
    audioEnd = '';
    audioRate = '';
    audioLoop = false;
    audioOpen = true;
  }

  function pickAudio(): void {
    audioInput?.click();
  }

  function onAudioChosen(event: Event): void {
    const input = event.currentTarget as HTMLInputElement;
    audioFile = input.files?.[0] ?? null;
    input.value = '';
  }

  /**
   * Uploads the chosen sound and inserts Anki's [sound:] tag with the segment attributes the dialog collected.
   * The upload has to finish inside the frame's saved caret, so the command carries the finished media reference.
   */
  async function insertAudio(): Promise<void> {
    const file = audioFile;
    const uploader = upload;
    if (!file || !uploader || !audioCanInsert) return;
    audioBusy = true;
    try {
      const media = await uploader(file);
      if (disposed) return;
      command('insertAudio', {
        media,
        start: audioStart.trim(),
        end: audioEnd.trim(),
        rate: audioRate.trim(),
        loop: audioLoop,
      });
      audioOpen = false;
    } catch (err) {
      if (disposed) return;
      toast.error($t(uploadErrorKeyFor(err)));
    } finally {
      audioBusy = false;
    }
  }

  /**
   * The frame's height; a fixed floor keeps an empty field usable, and the iframe never grows
   * unbounded because the frame reports its own content height.
   */
  const frameHeight = $derived(Math.max(height, 80));

  function isRecord(value: unknown): value is Record<string, unknown> {
    return typeof value === 'object' && value !== null;
  }

  async function handleUpload(id: string, file: File): Promise<void> {
    if (!upload) {
      send({ type: 'uploadError', id, code: 'no_uploader' });
      return;
    }
    try {
      const media = await upload(file);
      if (disposed) return;
      send({ type: 'uploaded', id, ...media });
    } catch (err) {
      if (disposed) return;
      send({ type: 'uploadError', id, code: err instanceof Error ? err.name : '' });
      toast.error($t(uploadErrorKeyFor(err)));
    }
  }

  function onMessage(event: MessageEvent): void {
    const frame = iframe;
    if (!frame || event.source !== frame.contentWindow) return;
    const data: unknown = event.data;
    if (!isRecord(data) || typeof data.type !== 'string') return;

    switch (data.type) {
      case 'ready':
        ready = true;
        loadedSignature = '';
        // The frame reloaded (or announced itself for the first time): send the current value even when
        // it is empty, because the presentation options still have to reach the new document.
        lastEcho = null;
        sendTheme();
        return;
      case 'change':
        if (typeof data.html !== 'string') return;
        lastEcho = data.html;
        onvalue(data.html);
        return;
      case 'height':
        if (typeof data.px === 'number' && Number.isFinite(data.px) && data.px >= 0 && data.px <= 100000) {
          height = Math.round(data.px);
        }
        return;
      case 'upload': {
        if (typeof data.id !== 'string' || !(data.file instanceof File) || typeof data.kind !== 'string') return;
        void handleUpload(data.id, data.file);
        return;
      }
      default:
        return;
    }
  }

  /** Clears the field for a new note; a sticky field keeps what it held. */
  export function reset(): void {
    if (options.sticky) return;
    sourceOpen = false;
    lastEcho = null;
    onvalue('');
    if (ready) sendLoad('');
    else loadedSignature = '';
  }

  /** Replaces the field's value through the frame, e.g. when the host loads another note. */
  export function load(html: string): void {
    lastEcho = html;
    if (ready) sendLoad(html);
  }

  // One load per value/presentation change. The echo of a `change` the frame itself reported is
  // skipped, so typing never reloads the document and never moves the caret.
  $effect(() => {
    void [value, plainText, rtl, font, size, description, ready];
    if (!ready) return;
    if (lastEcho !== null && value === lastEcho) {
      loadedSignature = signature(value);
      return;
    }
    if (signature(value) === loadedSignature) return;
    sendLoad(value);
  });

  $effect(() => {
    void document.documentElement.classList.contains('dark');
    sendTheme();
    const observer = new MutationObserver(() => sendTheme());
    observer.observe(document.documentElement, { attributes: true, attributeFilter: ['class'] });
    return () => observer.disconnect();
  });

  $effect(() => {
    window.addEventListener('message', onMessage);
    return () => {
      disposed = true;
      window.removeEventListener('message', onMessage);
    };
  });
</script>

<div class="field-frame @container" data-testid="{testId}-frame">
  {#if !plainText}
    <div
      class="field-frame-header flex flex-wrap items-center gap-x-1 gap-y-1 px-1.5 py-1 @max-sm:gap-x-3"
      role="group"
      aria-label={$t('editor.toolbar', { field: label })}
      data-testid="{testId}-toolbar"
    >
      <div class="flex items-center gap-0.5">
        {#each inlineActions as action (action.id)}
          <button
            type="button"
            class={toolbarButton}
            data-testid="{testId}-format-{action.id}"
            aria-label={$t(action.label, { field: label })}
            title={$t(action.label, { field: label })}
            aria-keyshortcuts={action.shortcut}
            onmousedown={(event) => event.preventDefault()}
            onclick={() => command(action.name, action.extra)}
          >
            <action.Icon class="size-4" aria-hidden="true" />
          </button>
        {/each}
        <label class="{toolbarButton} relative" title={$t('editor.text_color', { field: label })}>
          <Palette class="size-4" aria-hidden="true" />
          <input
            type="color"
            class="absolute inset-0 cursor-pointer opacity-0"
            data-testid="{testId}-text-color"
            aria-label={$t('editor.text_color', { field: label })}
            oninput={(event) => applyColor('textColor', event)}
          />
        </label>
        <label class="{toolbarButton} relative" title={$t('editor.highlight', { field: label })}>
          <Highlighter class="size-4" aria-hidden="true" />
          <input
            type="color"
            class="absolute inset-0 cursor-pointer opacity-0"
            data-testid="{testId}-highlight"
            aria-label={$t('editor.highlight', { field: label })}
            value="#fff59d"
            oninput={(event) => applyColor('highlight', event)}
          />
        </label>
      </div>
      <span class={separator} aria-hidden="true"></span>
      <div class="flex items-center gap-0.5">
        {#each listActions as action (action.id)}
          <button
            type="button"
            class={toolbarButton}
            data-testid="{testId}-format-{action.id}"
            aria-label={$t(action.label, { field: label })}
            title={$t(action.label, { field: label })}
            onmousedown={(event) => event.preventDefault()}
            onclick={() => command(action.name, action.extra)}
          >
            <action.Icon class="size-4" aria-hidden="true" />
          </button>
        {/each}
      </div>
      <span class={separator} aria-hidden="true"></span>
      <div class="flex items-center gap-0.5">
        {#each alignActions as action (action.id)}
          <button
            type="button"
            class={toolbarButton}
            data-testid="{testId}-format-{action.id}"
            aria-label={$t(action.label, { field: label })}
            title={$t(action.label, { field: label })}
            onmousedown={(event) => event.preventDefault()}
            onclick={() => command(action.name, action.extra)}
          >
            <action.Icon class="size-4" aria-hidden="true" />
          </button>
        {/each}
        {#each indentActions as action (action.id)}
          <button
            type="button"
            class={toolbarButton}
            data-testid="{testId}-format-{action.id}"
            aria-label={$t(action.label, { field: label })}
            title={$t(action.label, { field: label })}
            onmousedown={(event) => event.preventDefault()}
            onclick={() => command(action.name, action.extra)}
          >
            <action.Icon class="size-4" aria-hidden="true" />
          </button>
        {/each}
      </div>
      <span class={separator} aria-hidden="true"></span>
      <div class="flex items-center gap-0.5">
        <button
          type="button"
          class={toolbarButton}
          data-testid="{testId}-insert-image"
          aria-label={$t('editor.insert_image', { field: label })}
          title={$t('editor.insert_image', { field: label })}
          onmousedown={(event) => event.preventDefault()}
          onclick={pickFile}
        >
          <ImagePlus class="size-4" aria-hidden="true" />
        </button>
        <button
          type="button"
          class={toolbarButton}
          data-testid="{testId}-insert-audio"
          aria-label={$t('editor.audio.open', { field: label })}
          title={$t('editor.audio.open', { field: label })}
          onmousedown={(event) => event.preventDefault()}
          onclick={openAudio}
        >
          <Music class="size-4" aria-hidden="true" />
        </button>
        {#each insertActions as action (action.id)}
          <button
            type="button"
            class={toolbarButton}
            data-testid="{testId}-format-{action.id}"
            aria-label={$t(action.label, { field: label })}
            title={$t(action.label, { field: label })}
            aria-keyshortcuts={action.shortcut}
            onmousedown={(event) => event.preventDefault()}
            onclick={() => command(action.name, action.extra)}
          >
            <action.Icon class="size-4" aria-hidden="true" />
          </button>
        {/each}
        <button
          type="button"
          class={toolbarButton}
          data-testid="{testId}-source-toggle"
          aria-label={$t('editor.source.toggle', { field: label })}
          title={$t('editor.source.toggle', { field: label })}
          aria-pressed={sourceOpen}
          onmousedown={(event) => event.preventDefault()}
          onclick={toggleSource}
        >
          <CodeXml class="size-4" aria-hidden="true" />
        </button>
      </div>
    </div>
  {/if}
  <div class="flex items-center justify-end gap-1 px-1.5 py-0.5">
    {#if options.collapsed}
      <button
        type="button"
        class={toolbarButton}
        data-testid="{testId}-collapse-toggle"
        aria-label={$t(expanded ? 'editor.collapsed.hide' : 'editor.collapsed.show', { field: label })}
        title={$t(expanded ? 'editor.collapsed.hide' : 'editor.collapsed.show', { field: label })}
        aria-expanded={expanded}
        onclick={() => (expanded = !expanded)}
      >
        {#if expanded}
          <ChevronUp class="size-4" aria-hidden="true" />
        {:else}
          <ChevronDown class="size-4" aria-hidden="true" />
        {/if}
      </button>
    {/if}
  </div>
  {#if expanded}
    <iframe
      bind:this={iframe}
      class="block w-full border-0 bg-transparent"
      sandbox="allow-scripts"
      allow="autoplay"
      title={label}
      {src}
      data-testid={testId}
      style:height={`${frameHeight}px`}
    ></iframe>
  {/if}
</div>

<input
  bind:this={fileInput}
  type="file"
  accept="image/*,audio/*,video/*"
  class="hidden"
  tabindex="-1"
  aria-hidden="true"
  data-testid="{testId}-file-input"
  onchange={onFileChosen}
/>

<input
  bind:this={audioInput}
  type="file"
  accept="audio/*"
  class="hidden"
  tabindex="-1"
  aria-hidden="true"
  data-testid="{testId}-audio-input"
  onchange={onAudioChosen}
/>

<Dialog
  bind:open={audioOpen}
  title={$t('editor.audio.open', { field: label })}
  description={$t('editor.audio.hint')}
  testId="{testId}-audio-dialog"
>
  <form class="space-y-4" onsubmit={(event) => { event.preventDefault(); void insertAudio(); }}>
    <div>
      <span class="block text-sm font-medium text-foreground">{$t('editor.audio.file')}</span>
      <div class="mt-1.5 flex flex-wrap items-center gap-2">
        <Button type="button" variant="outline" size="sm" testId="{testId}-audio-choose" onclick={pickAudio}>
          {$t('editor.audio.choose')}
        </Button>
        <span class="min-w-0 truncate text-sm text-muted-foreground" data-testid="{testId}-audio-name">
          {audioFile?.name ?? $t('editor.audio.none')}
        </span>
      </div>
    </div>
    <div class="grid grid-cols-2 gap-3 sm:grid-cols-3">
      <label class="block text-sm font-medium text-foreground">
        <span>{$t('editor.audio.start')}</span>
        <input
          class="field-input mt-1.5 block w-full text-sm"
          data-testid="{testId}-audio-start"
          inputmode="decimal"
          spellcheck="false"
          aria-invalid={!segmentNumber(audioStart)}
          bind:value={audioStart}
        />
      </label>
      <label class="block text-sm font-medium text-foreground">
        <span>{$t('editor.audio.end')}</span>
        <input
          class="field-input mt-1.5 block w-full text-sm"
          data-testid="{testId}-audio-end"
          inputmode="decimal"
          spellcheck="false"
          aria-invalid={!segmentNumber(audioEnd) || audioRangeInvalid}
          bind:value={audioEnd}
        />
      </label>
      <label class="block text-sm font-medium text-foreground">
        <span>{$t('editor.audio.rate')}</span>
        <input
          class="field-input mt-1.5 block w-full text-sm"
          data-testid="{testId}-audio-rate"
          inputmode="decimal"
          spellcheck="false"
          aria-invalid={!segmentNumber(audioRate)}
          bind:value={audioRate}
        />
      </label>
    </div>
    {#if audioNumberInvalid}
      <p role="alert" class="text-sm text-destructive-foreground" data-testid="{testId}-audio-number-error">
        {$t('editor.audio.bad_number')}
      </p>
    {:else if audioRangeInvalid}
      <p role="alert" class="text-sm text-destructive-foreground" data-testid="{testId}-audio-range-error">
        {$t('editor.audio.bad_range')}
      </p>
    {/if}
    <div class="flex items-center gap-2">
      <Checkbox
        testId="{testId}-audio-loop"
        checked={audioLoop}
        onCheckedChange={(checked) => (audioLoop = checked)}
        label={$t('editor.audio.loop')}
      />
      <span class="text-sm text-foreground/80">{$t('editor.audio.loop')}</span>
    </div>
    <div class="flex items-center justify-end gap-2">
      <Button type="button" variant="ghost" size="sm" testId="{testId}-audio-cancel" onclick={() => (audioOpen = false)}>
        {$t('editor.audio.cancel')}
      </Button>
      <Button type="submit" variant="primary" size="sm" testId="{testId}-audio-submit" disabled={!audioCanInsert} loading={audioBusy}>
        {$t('editor.audio.insert')}
      </Button>
    </div>
  </form>
</Dialog>
