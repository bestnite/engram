import {
  alignBlock,
  applyStyle,
  clearFormatting,
  clozeNumber,
  currentRange,
  endRange,
  escapeAttribute,
  escapeHtml,
  htmlToPlainText,
  indentBlock,
  insertHtml,
  pasteHtml,
  plainTextToHtml,
  rangeInRoot,
  toggleInline,
  toggleList,
  wrapWithMarkers,
  type AlignValue,
  type EditingContext,
} from './commands';

/**
 * The rich-text editing frame runtime.
 *
 * The frame document is deliberately tiny: the parent page sends the field's HTML with `load`,
 * the toolbar's commands with `command`, and the theme with `theme`; the runtime edits its own
 * document and posts the resulting HTML back with `change`. It renders no Svelte component and
 * reads no cookie, exactly like the card frame. Untrusted field HTML is only ever parsed inside
 * this sandboxed, opaque-origin document.
 *
 * Every incoming message is accepted only from the embedding page (`event.source` must be the
 * parent window) and every field is type-checked.
 *
 * The HTML source view is a `<textarea>` inside this frame rather than one in the parent: the frame
 * already owns the field's value and mode, and keeping the textarea here means a toggle never has
 * to ship the HTML across the boundary and parse it again just to show it.
 */

export interface EditFrameOptions {
  win?: Window;
  doc?: Document;
  /** The embedding window; defaults to `win.parent` and exists so tests can supply their own. */
  parent?: Window | null;
}

export type EditMode = 'rich' | 'plain' | 'source';

export interface LoadMessage {
  type: 'load';
  html: string;
  plainText: boolean;
  rtl: boolean;
  font: string;
  size: number;
  description: string;
  dark: boolean;
  source: boolean;
}

export interface CommandMessage {
  type: 'command';
  name: string;
  color?: string;
  value?: string;
  delta?: number;
  media?: MediaReference;
}

/** The media a command inserts: a same-origin URL, the deck media name and the detected mime type. */
export interface MediaReference {
  url: string;
  name: string;
  mime: string;
}

export interface UploadedMessage {
  type: 'uploaded';
  id: string;
  url: string;
  name: string;
  mime: string;
}

export interface UploadErrorMessage {
  type: 'uploadError';
  id: string;
  code: string;
}

export interface EditFrameRuntime {
  /** Handles one parent message. */
  handleMessage(event: MessageEvent): void;
  /** Posts `ready` once, after the runtime has installed its listeners. */
  signalReady(): void;
  destroy(): void;
  /** The current field value, serialised the way the form submits it. */
  value(): string;
  /** Reports the current value to the parent; the runtime calls it after every edit. */
  postChange(): void;
}

function isRecord(value: unknown): value is Record<string, unknown> {
  return typeof value === 'object' && value !== null;
}

function isString(value: unknown): value is string {
  return typeof value === 'string';
}

function parseLoad(data: Record<string, unknown>): LoadMessage | null {
  const { html, plainText, rtl, font, size, description, dark, source } = data;
  if (
    !isString(html) ||
    typeof plainText !== 'boolean' ||
    typeof rtl !== 'boolean' ||
    !isString(font) ||
    typeof size !== 'number' ||
    !Number.isFinite(size) ||
    !isString(description) ||
    typeof dark !== 'boolean' ||
    typeof source !== 'boolean'
  ) {
    return null;
  }
  return { type: 'load', html, plainText, rtl, font, size, description, dark, source };
}

function parseCommand(data: Record<string, unknown>): CommandMessage | null {
  if (!isString(data.name)) return null;
  let media: MediaReference | undefined;
  if (isRecord(data.media) && isString(data.media.url) && isString(data.media.name) && isString(data.media.mime)) {
    media = { url: data.media.url, name: data.media.name, mime: data.media.mime };
  }
  return {
    type: 'command',
    name: data.name,
    color: isString(data.color) ? data.color : undefined,
    value: isString(data.value) ? data.value : undefined,
    delta: typeof data.delta === 'number' && Number.isFinite(data.delta) ? data.delta : undefined,
    media,
  };
}

function parseUploaded(data: Record<string, unknown>): UploadedMessage | null {
  const { id, url, name, mime } = data;
  if (!isString(id) || !isString(url) || !isString(name) || !isString(mime)) return null;
  return { type: 'uploaded', id, url, name, mime };
}

export function createRuntime(options: EditFrameOptions = {}): EditFrameRuntime {
  const win = (options.win ?? window) as Window;
  const doc = options.doc ?? win.document;
  const parent = (options.parent ?? (win.parent as Window | null) ?? win) as Window | null;

  const root = doc.getElementById('editor') ?? doc.createElement('div');
  if (!root.parentNode) doc.body?.appendChild(root);
  root.classList.add('edit-field');
  root.setAttribute('contenteditable', 'true');
  root.setAttribute('role', 'textbox');
  root.setAttribute('aria-multiline', 'true');

  const placeholder = doc.createElement('div');
  placeholder.className = 'edit-placeholder';
  placeholder.setAttribute('aria-hidden', 'true');

  const plainArea = doc.createElement('textarea');
  plainArea.className = 'edit-plain';
  plainArea.spellcheck = true;

  const sourceArea = doc.createElement('textarea');
  sourceArea.className = 'edit-source';
  sourceArea.spellcheck = false;

  const surface = doc.createElement('div');
  surface.className = 'edit-surface';
  doc.body.appendChild(surface);
  surface.append(placeholder, root, plainArea, sourceArea);

  const ctx: EditingContext = { doc, root };

  let ready = false;
  let mode: EditMode = 'rich';
  let plainText = false;
  let description = '';
  /** The Shift key state, tracked because a paste event itself carries no modifier state. */
  let shiftPressed = false;
  /** The authoritative value. It is updated on every edit, but a load stores the input verbatim. */
  let html = '';
  /** The selection last seen inside the root; a toolbar click can move focus out of the frame. */
  let savedRange: Range | null = null;
  let uploadSequence = 0;
  const pendingUploads = new Map<string, { range: Range | null }>();
  let lastHeight = -1;
  let observer: ResizeObserver | null = null;

  function post(message: Record<string, unknown>): void {
    parent?.postMessage(message, '*');
  }

  function isRich(): boolean {
    return mode === 'rich';
  }

  /** Serialises the rich surface; an empty editor is the empty string, never `<br>`. */
  function serializeRoot(): string {
    if (root.textContent === '' && !root.querySelector('img, audio, video, hr')) return '';
    return root.innerHTML;
  }

  function value(): string {
    if (mode === 'plain') return plainTextToHtml(plainArea.value);
    if (mode === 'source') return sourceArea.value;
    return serializeRoot();
  }

  function rememberRange(): void {
    const selection = doc.getSelection();
    if (!selection || selection.rangeCount === 0) return;
    const range = selection.getRangeAt(0);
    if (rangeInRoot(ctx, range)) savedRange = range.cloneRange();
  }

  /** The range a command must act on: the live selection, else the last one, else the end. */
  function commandRange(): Range {
    const range = currentRange(ctx, savedRange);
    savedRange = range.cloneRange();
    return range;
  }

  function syncPlaceholder(): void {
    placeholder.textContent = description;
    // A plain-text or source textarea carries the placeholder itself; the overlay is for the
    // contenteditable surface, which has no native placeholder.
    const empty = isRich() && serializeRoot() === '';
    placeholder.classList.toggle('is-hidden', !empty || description === '');
    placeholder.classList.toggle('is-source', mode !== 'rich');
  }

  function syncHeight(): void {
    const px = Math.max(0, Math.round(root.scrollHeight || root.offsetHeight || 0));
    if (px === lastHeight) return;
    lastHeight = px;
    post({ type: 'height', px });
  }

  function postChange(): void {
    html = value();
    syncPlaceholder();
    syncHeight();
    post({ type: 'change', html });
  }

  /** Applies the loaded presentation options to the document. */
  function applyPresentation(message: LoadMessage): void {
    doc.documentElement.classList.toggle('dark', message.dark);
    doc.documentElement.style.colorScheme = message.dark ? 'dark' : 'light';
    root.dir = message.rtl ? 'rtl' : 'ltr';
    root.style.fontFamily = message.font;
    root.style.fontSize = message.size > 0 ? `${message.size}px` : '';
    plainArea.dir = message.rtl ? 'rtl' : 'ltr';
    plainArea.style.fontFamily = message.font;
    plainArea.style.fontSize = message.size > 0 ? `${message.size}px` : '';
    sourceArea.dir = message.rtl ? 'rtl' : 'ltr';
    description = message.description;
    plainArea.placeholder = message.description;
  }

  function setMode(next: EditMode): void {
    if (next === mode) return;
    // `html` is already the authoritative value: every edit and command updates it, and a load
    // stores the input verbatim. Re-reading the DOM here would reserialise (and normalise) markup
    // that the source view must show unchanged.
    mode = next;
    root.classList.toggle('is-hidden', mode !== 'rich');
    plainArea.classList.toggle('is-hidden', mode !== 'plain');
    sourceArea.classList.toggle('is-hidden', mode !== 'source');
    if (mode === 'rich') {
      root.innerHTML = html;
    } else if (mode === 'plain') {
      plainArea.value = htmlToPlainText(ctx, html);
    } else {
      sourceArea.value = html;
    }
    syncPlaceholder();
    syncHeight();
    if (mode === 'rich') root.focus();
    else if (mode === 'plain') plainArea.focus();
    else sourceArea.focus();
  }

  function load(message: LoadMessage): void {
    html = message.html;
    plainText = message.plainText;
    applyPresentation(message);
    const next: EditMode = message.source ? 'source' : message.plainText ? 'plain' : 'rich';
    mode = next;
    root.classList.toggle('is-hidden', mode !== 'rich');
    plainArea.classList.toggle('is-hidden', mode !== 'plain');
    sourceArea.classList.toggle('is-hidden', mode !== 'source');
    if (mode === 'rich') root.innerHTML = html;
    else if (mode === 'plain') plainArea.value = htmlToPlainText(ctx, html);
    else sourceArea.value = html;
    savedRange = null;
    syncPlaceholder();
    syncHeight();
  }

  /** Inserts the markup for one uploaded file; images render now, sounds use Anki's tag. */
  function insertMedia(url: string, name: string, mime: string): void {
    if (mime.startsWith('image/')) {
      insertHtml(ctx, `<img src="${escapeAttribute(url || name)}">`, savedRange);
      return;
    }
    if (mime.startsWith('audio/') || mime.startsWith('video/')) {
      insertHtml(ctx, `[sound:${escapeHtml(name)}]`, savedRange);
      return;
    }
    insertHtml(ctx, `<a href="${escapeAttribute(url)}">${escapeHtml(name)}</a>`, savedRange);
  }

  function uploadFile(file: File): void {
    const kind = file.type.startsWith('image/') ? 'image' : file.type.startsWith('audio/') || file.type.startsWith('video/') ? 'sound' : 'file';
    const id = `u${uploadSequence++}`;
    pendingUploads.set(id, { range: savedRange?.cloneRange() ?? null });
    post({ type: 'upload', id, file, kind });
  }

  function command(message: CommandMessage): void {
    if (!isRich()) return;
    const range = commandRange();
    switch (message.name) {
      case 'bold':
        toggleInline(ctx, 'bold', range);
        break;
      case 'italic':
        toggleInline(ctx, 'italic', range);
        break;
      case 'underline':
        toggleInline(ctx, 'underline', range);
        break;
      case 'superscript':
        toggleInline(ctx, 'superscript', range);
        break;
      case 'subscript':
        toggleInline(ctx, 'subscript', range);
        break;
      case 'textColor':
        applyStyle(ctx, 'color', message.color ?? '', range);
        break;
      case 'highlight':
        applyStyle(ctx, 'background-color', message.color ?? '', range);
        break;
      case 'clearFormatting':
        clearFormatting(ctx, range);
        break;
      case 'unorderedList':
        toggleList(ctx, 'ul', range);
        break;
      case 'orderedList':
        toggleList(ctx, 'ol', range);
        break;
      case 'align':
        if (message.value === 'left' || message.value === 'center' || message.value === 'right' || message.value === 'justify') {
          alignBlock(ctx, message.value as AlignValue, range);
        }
        break;
      case 'indent':
        indentBlock(ctx, message.delta === -1 ? -1 : 1, range);
        break;
      case 'mathjaxInline':
        wrapWithMarkers(ctx, '\\(', '\\)', range);
        break;
      case 'mathjaxBlock':
        wrapWithMarkers(ctx, '\\[', '\\]', range);
        break;
      case 'latex':
        wrapWithMarkers(ctx, '[latex]', '[/latex]', range);
        break;
      case 'cloze':
        wrapWithMarkers(ctx, `{{c${clozeNumber(html, true)}::`, '}}', range);
        break;
      case 'clozeSame':
        wrapWithMarkers(ctx, `{{c${clozeNumber(html, false)}::`, '}}', range);
        break;
      case 'insertMedia':
        if (message.media) insertMedia(message.media.url, message.media.name, message.media.mime);
        break;
      default:
        return;
    }
    rememberRange();
    postChange();
  }

  /** Ctrl+Shift+C adds the next cloze number; Ctrl+Alt+Shift+C reuses the highest one. */
  function handleKeydown(event: KeyboardEvent): void {
    shiftPressed = event.shiftKey;
    if (!isRich() || !(event.ctrlKey || event.metaKey) || !event.shiftKey) return;
    const key = event.key.toLowerCase();
    if (key !== 'c') return;
    event.preventDefault();
    command({ type: 'command', name: event.altKey ? 'clozeSame' : 'cloze' });
  }

  function handleKeyup(event: KeyboardEvent): void {
    shiftPressed = event.shiftKey;
  }

  /**
   * Paste keeps the pasted markup; Shift strips it. That is Anki 26.09.3's default: its editor reads
   * `PASTE_STRIPS_FORMATTING` and inverts it while Shift is held
   * (`ts/routes/editor/rich-text-input/data-transfer.ts`), and `rslib/src/config/bool.rs` leaves the key
   * out of the list that defaults to true, so the setting is off unless the user enables it.
   */
  function handlePaste(event: ClipboardEvent): void {
    if (!isRich()) return;
    event.preventDefault();
    rememberRange();
    const data = event.clipboardData;
    const files = data ? Array.from(data.files ?? []) : [];
    if (files.length > 0) {
      files.forEach(uploadFile);
      return;
    }
    const pastedHtml = data?.getData('text/html') ?? '';
    const text = data?.getData('text/plain') ?? '';
    const keepFormatting = !shiftPressed;
    const source = pastedHtml !== '' ? pastedHtml : plainTextToHtml(text);
    insertHtml(ctx, pasteHtml(ctx, source, keepFormatting), savedRange);
    postChange();
  }

  function handleDrop(event: DragEvent): void {
    if (!isRich()) return;
    event.preventDefault();
    root.focus();
    rememberRange();
    const data = event.dataTransfer;
    const files = data ? Array.from(data.files ?? []) : [];
    if (files.length > 0) {
      files.forEach(uploadFile);
      return;
    }
    const text = data?.getData('text/plain') ?? '';
    const pastedHtml = data?.getData('text/html') ?? '';
    const keepFormatting = !event.shiftKey;
    const source = pastedHtml !== '' ? pastedHtml : plainTextToHtml(text);
    insertHtml(ctx, pasteHtml(ctx, source, keepFormatting), savedRange);
    postChange();
  }

  function onUploaded(message: UploadedMessage): void {
    const pending = pendingUploads.get(message.id);
    if (!pending) return;
    pendingUploads.delete(message.id);
    savedRange = pending.range;
    insertMedia(message.url, message.name, message.mime);
    savedRange = null;
    postChange();
  }

  function onUploadError(message: UploadErrorMessage): void {
    if (!pendingUploads.has(message.id)) return;
    pendingUploads.delete(message.id);
    savedRange = null;
  }

  function handleMessage(event: MessageEvent): void {
    if ((event.source as unknown) !== (parent as unknown)) return;
    const data: unknown = event.data;
    if (!isRecord(data) || !isString(data.type)) return;

    switch (data.type) {
      case 'load': {
        const message = parseLoad(data);
        if (message) load(message);
        return;
      }
      case 'command': {
        const message = parseCommand(data);
        if (message) command(message);
        return;
      }
      case 'source':
        if (typeof data.open === 'boolean' && !plainText) setMode(data.open ? 'source' : 'rich');
        return;
      case 'theme':
        if (typeof data.dark === 'boolean') {
          doc.documentElement.classList.toggle('dark', data.dark);
          doc.documentElement.style.colorScheme = data.dark ? 'dark' : 'light';
        }
        return;
      case 'uploaded': {
        const message = parseUploaded(data);
        if (message) onUploaded(message);
        return;
      }
      case 'uploadError': {
        if (isString(data.id) && isString(data.code)) onUploadError({ type: 'uploadError', id: data.id, code: data.code });
        return;
      }
      default:
        return;
    }
  }

  function onMessage(event: MessageEvent): void {
    handleMessage(event);
  }

  function onInput(): void {
    if (!isRich()) return;
    rememberRange();
    postChange();
  }

  function onPlainInput(): void {
    html = plainTextToHtml(plainArea.value);
    syncPlaceholder();
    syncHeight();
    post({ type: 'change', html });
  }

  function onSourceInput(): void {
    html = sourceArea.value;
    syncPlaceholder();
    syncHeight();
    post({ type: 'change', html });
  }

  function onSelectionChange(): void {
    if (isRich()) rememberRange();
  }

  /** Tracks Shift at the window level, because the paste event itself reports no modifier state. */
  function trackShift(event: KeyboardEvent): void {
    shiftPressed = event.shiftKey;
  }

  root.addEventListener('input', onInput);
  root.addEventListener('keydown', handleKeydown);
  root.addEventListener('paste', handlePaste);
  root.addEventListener('drop', handleDrop);
  root.addEventListener('dragover', (event) => event.preventDefault());
  root.addEventListener('keyup', rememberRange);
  root.addEventListener('keyup', handleKeyup);
  root.addEventListener('mouseup', rememberRange);
  plainArea.addEventListener('input', onPlainInput);
  sourceArea.addEventListener('input', onSourceInput);
  doc.addEventListener('selectionchange', onSelectionChange);
  win.addEventListener('keydown', trackShift);
  win.addEventListener('keyup', trackShift);
  win.addEventListener('message', onMessage);

  const ResizeObserverCtor = (win as unknown as { ResizeObserver?: typeof ResizeObserver }).ResizeObserver;
  if (typeof ResizeObserverCtor === 'function') {
    observer = new ResizeObserverCtor(() => syncHeight());
    observer.observe(root);
  }

  return {
    handleMessage,
    signalReady() {
      if (ready) return;
      ready = true;
      post({ type: 'ready' });
    },
    destroy() {
      root.removeEventListener('input', onInput);
      root.removeEventListener('keydown', handleKeydown);
      root.removeEventListener('paste', handlePaste);
      root.removeEventListener('drop', handleDrop);
      root.removeEventListener('keyup', rememberRange);
      root.removeEventListener('keyup', handleKeyup);
      root.removeEventListener('mouseup', rememberRange);
      plainArea.removeEventListener('input', onPlainInput);
      sourceArea.removeEventListener('input', onSourceInput);
      doc.removeEventListener('selectionchange', onSelectionChange);
      win.removeEventListener('keydown', trackShift);
      win.removeEventListener('keyup', trackShift);
      win.removeEventListener('message', onMessage);
      observer?.disconnect();
      observer = null;
    },
    value() {
      return value();
    },
    postChange,
  };
}

/** The caret at the end, exported for callers that need to place it after a full re-render. */
export { endRange };
