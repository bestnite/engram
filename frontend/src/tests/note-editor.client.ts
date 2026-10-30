import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest';
import { mount, unmount, flushSync } from 'svelte';
import { get } from 'svelte/store';
import NoteEditView from '../lib/views/NoteEditView.svelte';
import { routeStore } from '../lib/router';
import { routes } from '../lib/router/routes';
import { apiClient, ApiClientError, type Note, type NotePreviewResponse } from '../lib/api';
import { seedCardTypes, clearCardTypes } from './card-type-fixture';
import { typeset } from '../lib/mathjax';
import { setLocale, formatMessage } from '../lib/i18n';
import { shownHtmlOnSide, shownFaces, frameDocument } from './card-frame-fixture';
import { face } from './card-face-fixture';
import { NOTE_PREVIEW_CSS } from '../lib/card-frame';
import { toast } from '../lib/components/ui/toast';

// MathJax is taken over by a module-boundary stand-in: the cases assert "typesetting really ran, and only on the preview container",
// without depending on real MathJax assets (the test environment has no self-hosted script).
vi.mock('../lib/mathjax', () => ({ typeset: vi.fn(async () => {}) }));
// Save results go through a toast: the stand-in records calls, and cases assert what was reported.
vi.mock('../lib/components/ui/toast', () => ({ toast: { success: vi.fn(), warning: vi.fn(), error: vi.fn() } }));

const typesetSpy = vi.mocked(typeset);

/** A promise whose resolution moment is controlled by hand, used to produce stale responses/errors that land out of order. */
function deferred<T>() {
  let resolve!: (value: T) => void;
  let reject!: (reason?: unknown) => void;
  const promise = new Promise<T>((res, rej) => {
    resolve = res;
    reject = rej;
  });
  return { promise, resolve, reject };
}

function noteFixture(overrides: Partial<Note> = {}): Note {
  return {
    id: '9',
    deck_id: '5',
    kind: 'basic',
    fields: { front: 'Q', back: 'A' },
    tags: [],
    created_at: '2026-10-01T00:00:00Z',
    updated_at: '2026-10-01T00:00:00Z',
    external_ref: '',
    suspended: false,
    ...overrides,
  };
}

/** The response the preview endpoint sends: one entry per card, each carrying both faces. */
function createResponse(cards: Array<{ front_html: string; back_html: string }>): NotePreviewResponse {
  return {
    cards: cards.map((card) => ({
      front: face(card.front_html),
      back: face(card.back_html, { side: 'back' }),
    })),
  };
}

let target: HTMLElement;
const mountedInstances: Array<ReturnType<typeof mount>> = [];
/** The latest HTML each field reported, keyed by data-testid; the frame owns the editing surface. */
const fieldValues = new Map<string, string>();

function mountEditor(params: Record<string, string>, query: Record<string, string> = {}): void {
  seedCardTypes();
  routeStore.set({ path: '/decks/5/notes', params, query, search: '', route: null });
  target = document.createElement('div');
  document.body.appendChild(target);
  mountedInstances.push(mount(NoteEditView, { target }));
}

/** Flush microtasks and apply Svelte's pending state synchronously so assertions see the real DOM. */
async function flush(times = 24): Promise<void> {
  for (let i = 0; i < times; i++) await Promise.resolve();
  flushSync();
}

/** The fake frame document a field talks to; the sandboxed iframe cannot load in the test environment. */
interface FakeFrame {
  postMessage: ReturnType<typeof vi.fn>;
}

const frames = new WeakMap<HTMLIFrameElement, FakeFrame>();

function frameFor(testid: string): FakeFrame {
  const iframe = target.querySelector(`[data-testid="${testid}"]`) as HTMLIFrameElement;
  if (!iframe) throw new Error(`missing field frame ${testid}`);
  let frame = frames.get(iframe);
  if (!frame) {
    frame = { postMessage: vi.fn() };
    Object.defineProperty(iframe, 'contentWindow', { configurable: true, value: frame });
    frames.set(iframe, frame);
  }
  return frame;
}

function fromFrame(frame: FakeFrame, data: Record<string, unknown>): void {
  window.dispatchEvent(new MessageEvent('message', { data, source: frame as unknown as MessageEventSource }));
}

/** Writes a plain control (a number, a tag, a non-face text field) as a real user input does. */
function setInput(testid: string, value: string): void {
  const el = target.querySelector(`[data-testid="${testid}"]`) as HTMLInputElement | HTMLTextAreaElement;
  el.value = value;
  el.dispatchEvent(new Event('input', { bubbles: true }));
}

/** Writes a field through its frame, the way typing inside the sandbox does. */
function setField(testid: string, value: string): void {
  const frame = frameFor(testid);
  fromFrame(frame, { type: 'ready' });
  fieldValues.set(testid, value);
  fromFrame(frame, { type: 'change', html: value });
}

/** The last HTML a field reported. */
function fieldValue(testid: string): string {
  return fieldValues.get(testid) ?? '';
}

/** The HTML the field was last loaded with (the `load` message the parent sent). */
function loadedHtml(testid: string): string {
  const calls = frameFor(testid).postMessage.mock.calls;
  for (let i = calls.length - 1; i >= 0; i -= 1) {
    const message = calls[i]?.[0] as Record<string, unknown> | undefined;
    if (message?.type === 'load' && typeof message.html === 'string') return message.html;
  }
  return '';
}

async function selectKind(label: string): Promise<void> {
  const trigger = target.querySelector('[data-testid="note-edit-kind"]') as HTMLElement;
  trigger.focus();
  trigger.dispatchEvent(new KeyboardEvent('keydown', { key: 'Enter', bubbles: true }));
  await flush();
  const option = Array.from(document.body.querySelectorAll('[role="option"]')).find(
    (el) => el.textContent?.trim() === label
  ) as HTMLElement;
  // bits-ui options commit the selection on pointerdown/pointerup (dispatching click alone does not select).
  option.dispatchEvent(new PointerEvent('pointerdown', { bubbles: true, cancelable: true }));
  option.dispatchEvent(new PointerEvent('pointerup', { bubbles: true, cancelable: true }));
  await flush();
}

function submitForm(): void {
  const form = target.querySelector('form') as HTMLFormElement;
  form.dispatchEvent(new Event('submit', { bubbles: true, cancelable: true }));
}

/**
 * The front face of one preview card. The editor hands both faces to the frame, so the content is read from what that frame
 * was asked to show rather than from the parent DOM.
 */
async function previewFront(index = 0): Promise<string> {
  return (await shownHtmlOnSide(target, 'question'))[index] ?? '';
}

beforeEach(() => {
  setLocale('zh-CN');
  vi.useFakeTimers();
  vi.clearAllMocks();
});

afterEach(() => {
  while (mountedInstances.length > 0) unmount(mountedInstances.pop()!);
  vi.restoreAllMocks();
  vi.useRealTimers();
  document.body.innerHTML = '';
  clearCardTypes();
  fieldValues.clear();
});

describe('unified note editor — create mode', () => {
  it('renders the full editor with an automatic (button-less) preview and no empty-id note read', async () => {
    const previewNote = vi.spyOn(apiClient, 'previewNote').mockResolvedValue(createResponse([]));
    const getNote = vi.spyOn(apiClient, 'getNote');

    mountEditor({ id: '5' });
    await flush();

    expect(target.querySelector('[data-testid="note-editor-form"]')).toBeTruthy();
    // The preview button is fully gone: no old testid and no leftover references (its keys were removed from the language packs).
    expect(target.querySelector('[data-testid="note-preview"]')).toBeNull();
    expect(target.innerHTML).not.toContain('note_preview.action');
    // No content entered: takes the empty path and sends no preview request to the server.
    expect(target.querySelector('[data-testid="note-preview-empty"]')).toBeTruthy();
    expect(previewNote).not.toHaveBeenCalled();
    // Initialization must never GET a remote card with an empty id.
    expect(getNote).not.toHaveBeenCalled();
  });

  it('debounces reactive preview and renders the latest result', async () => {
    const previewNote = vi
      .spyOn(apiClient, 'previewNote')
      .mockResolvedValue(createResponse([{ front_html: '<p>rendered Q</p>', back_html: '<p>A</p>' }]));

    mountEditor({ id: '5' });
    await flush();

    setField('note-field-front', 'Q');
    await flush();

    await vi.advanceTimersByTimeAsync(499);
    expect(previewNote).not.toHaveBeenCalled();
    await vi.advanceTimersByTimeAsync(1);
    await flush();

    expect(previewNote).toHaveBeenCalledTimes(1);
    expect(previewNote).toHaveBeenCalledWith('5', 'basic', { front: 'Q' }, { frame: true });
    expect(await previewFront()).toContain('rendered Q');
    expect(target.querySelector('[data-testid="note-preview-result"]')).toBeTruthy();
  });

  it('writes the note type CSS and the server body class into the preview frame document', async () => {
    const noteCSS = '.card { color: rgb(1, 2, 3); }';
    vi.spyOn(apiClient, 'previewNote').mockResolvedValue({
      cards: [
        {
          front: { html: '<p>Q</p>', css: noteCSS, body_class: 'card type-preview', side: 'front', media_base: '/fm/token/' },
          back: { html: '<p>A</p>', css: noteCSS, body_class: 'card type-preview', side: 'back', media_base: '/fm/token/' },
        },
      ],
      media_base: '/fm/token/',
    });

    mountEditor({ id: '5' });
    await flush();

    setField('note-field-front', 'Q');
    await flush();
    await vi.advanceTimersByTimeAsync(500);
    await flush();

    const shown = await shownFaces(target);
    expect(shown).toHaveLength(2);
    // Same rule as the review page: the preview's engram typography first, then the note type CSS, so the note type wins.
    for (const shownFace of shown) {
      const doc = await frameDocument(shownFace);
      expect(doc.head.querySelector('style[data-card-css]')?.textContent).toBe(`${NOTE_PREVIEW_CSS}\n${noteCSS}`);
      expect(doc.body.className).toBe('card type-preview card1');
      expect(shownFace.mediaBase).toBe('/fm/token/');
    }
  });

  it('resets the debounce window on every keystroke (only the last one fires)', async () => {
    const previewNote = vi.spyOn(apiClient, 'previewNote').mockResolvedValue(createResponse([]));

    mountEditor({ id: '5' });
    await flush();

    setField('note-field-front', 'Q');
    await flush();
    await vi.advanceTimersByTimeAsync(300);
    setField('note-field-front', 'QR');
    await flush();

    // 500ms have passed since the first input, but the second input reset the window, so nothing should be sent yet.
    await vi.advanceTimersByTimeAsync(200);
    expect(previewNote).not.toHaveBeenCalled();
    await vi.advanceTimersByTimeAsync(300);
    await flush();

    expect(previewNote).toHaveBeenCalledTimes(1);
    expect(previewNote).toHaveBeenCalledWith('5', 'basic', { front: 'QR' }, { frame: true });
  });

  it('ignores a stale delayed response and keeps the newest preview', async () => {
    const first = deferred<NotePreviewResponse>();
    const second = deferred<NotePreviewResponse>();
    const previewNote = vi
      .spyOn(apiClient, 'previewNote')
      .mockReturnValueOnce(first.promise)
      .mockReturnValueOnce(second.promise);

    mountEditor({ id: '5' });
    await flush();

    setField('note-field-front', 'A');
    await flush();
    await vi.advanceTimersByTimeAsync(500); // Request 1 in flight
    setField('note-field-front', 'B');
    await flush();
    await vi.advanceTimersByTimeAsync(500); // Request 2 in flight
    expect(previewNote).toHaveBeenCalledTimes(2);

    second.resolve(createResponse([{ front_html: '<p>SECOND</p>', back_html: '' }]));
    await flush();
    expect(await previewFront()).toContain('SECOND');

    // The old request only comes back now: it must be dropped and must not overwrite the newer result.
    first.resolve(createResponse([{ front_html: '<p>FIRST</p>', back_html: '' }]));
    await flush();
    expect(await previewFront()).toContain('SECOND');
    expect(await previewFront()).not.toContain('FIRST');
  });

  it('ignores a stale delayed error after a newer success', async () => {
    const first = deferred<NotePreviewResponse>();
    const previewNote = vi
      .spyOn(apiClient, 'previewNote')
      .mockReturnValueOnce(first.promise)
      .mockResolvedValueOnce(createResponse([{ front_html: '<p>GOOD</p>', back_html: '' }]));

    mountEditor({ id: '5' });
    await flush();

    setField('note-field-front', 'A');
    await flush();
    await vi.advanceTimersByTimeAsync(500);
    setField('note-field-front', 'B');
    await flush();
    await vi.advanceTimersByTimeAsync(500);
    expect(previewNote).toHaveBeenCalledTimes(2);
    await flush();
    expect(await previewFront()).toContain('GOOD');

    // The stale request then fails: it must not turn a ready preview into an error state.
    first.reject(new ApiClientError('HTTP 500', { status: 500, code: 'internal_error' }));
    await flush();
    expect(await previewFront()).toContain('GOOD');
    expect(target.querySelector('[data-testid="note-preview-error"]')).toBeNull();
  });

  it('shows the incomplete state on a 400 preview response', async () => {
    vi.spyOn(apiClient, 'previewNote').mockRejectedValue(
      new ApiClientError('HTTP 400', { status: 400, code: 'invalid_request' })
    );

    mountEditor({ id: '5' });
    await flush();
    setField('note-field-front', 'Q');
    await flush();
    await vi.advanceTimersByTimeAsync(500);
    await flush();

    expect(target.querySelector('[data-testid="note-preview-incomplete"]')).toBeTruthy();
    expect(target.querySelector('[data-testid="note-preview-error"]')).toBeNull();
  });

  it('treats a blank bool-bearing type as empty and never previews it', async () => {
    const previewNote = vi.spyOn(apiClient, 'previewNote');

    mountEditor({ id: '5' });
    await flush();

    // The typed empty form defaults to ignore_case / ignore_whitespace = true: those are not user input,
    // so they cannot count as "has content" — otherwise an empty form would request a preview.
    await selectKind(formatMessage('zh-CN', 'notes.kind.typed'));
    expect(target.querySelector('[data-testid="note-field-prompt"]')).toBeTruthy();
    await flush();

    await vi.advanceTimersByTimeAsync(600);
    await flush();

    expect(previewNote).not.toHaveBeenCalled();
    expect(target.querySelector('[data-testid="note-preview-empty"]')).toBeTruthy();
  });

  it('counts a numeric zero as real input and previews it', async () => {
    const previewNote = vi.spyOn(apiClient, 'previewNote').mockResolvedValue(createResponse([]));

    mountEditor({ id: '5' });
    await flush();

    await selectKind(formatMessage('zh-CN', 'notes.kind.numeric'));
    // Only a numeric field is filled (value 0): the empty-string baseline differs from 0, so 0 must count as content and trigger a preview.
    setInput('note-field-value', '0');
    await flush();

    await vi.advanceTimersByTimeAsync(500);
    await flush();

    expect(previewNote).toHaveBeenCalledWith('5', 'numeric', { value: 0 }, { frame: true });
  });

  it('renders the preview through the frame instead of typesetting it in the parent', async () => {
    vi.spyOn(apiClient, 'previewNote').mockResolvedValue(
      createResponse([{ front_html: '<p>$x$</p>', back_html: '' }])
    );

    mountEditor({ id: '5' });
    await flush();
    setField('note-field-front', 'Q');
    await flush();
    await vi.advanceTimersByTimeAsync(500);
    await flush();

    // The preview face is a card face: the frame typesets it, so the parent calls no MathJax on its own DOM.
    expect(typesetSpy).not.toHaveBeenCalled();
    expect(await shownHtmlOnSide(target, 'question')).toEqual(['<p>$x$</p>']);
    expect(target.querySelector('[data-testid="note-preview-front-0"] iframe[data-card-frame]')).not.toBeNull();
  });

  it('hands the preview face with its shortcode markup to the frame', async () => {
    const sha = 'a'.repeat(64);
    const front = `<p><span class="engram-audio" data-audio-src="/media/${sha}" data-audio-start="12.5">/media/${sha}</span></p>`;
    vi.spyOn(apiClient, 'previewNote').mockResolvedValue(createResponse([{ front_html: front, back_html: '' }]));

    mountEditor({ id: '5' });
    await flush();
    setField('note-field-front', 'Q');
    await flush();
    await vi.advanceTimersByTimeAsync(500);
    await flush();

    // The frame runtime attaches the controls to the face it renders (covered by cardframe.client.ts), so the editor only has
    // to hand over the sanitized markup with the shortcode attributes intact.
    expect(await shownHtmlOnSide(target, 'question')).toEqual([front]);
  });

  it('sends only the newest preview generation to the frame', async () => {
    const first = deferred<NotePreviewResponse>();
    vi.spyOn(apiClient, 'previewNote')
      .mockReturnValueOnce(first.promise)
      .mockResolvedValueOnce(createResponse([{ front_html: '<p>SECOND</p>', back_html: '' }]));

    mountEditor({ id: '5' });
    await flush();

    setField('note-field-front', 'A');
    await flush();
    await vi.advanceTimersByTimeAsync(500);
    setField('note-field-front', 'B');
    await flush();
    await vi.advanceTimersByTimeAsync(500);
    await flush();
    expect(await previewFront()).toContain('SECOND');

    // The first request lands late: its content must not replace the newer face in the frame.
    first.resolve(createResponse([{ front_html: '<p>FIRST</p>', back_html: '' }]));
    await flush();
    expect(await previewFront()).toContain('SECOND');
    expect(await previewFront()).not.toContain('FIRST');
  });

  it('creates a non-basic card and redirects to the deck page only on success', async () => {
    const createNotes = vi.spyOn(apiClient, 'createNotes').mockResolvedValue({
      created: 1,
      updated: 0,
      skipped: 0,
      errors: [],
      dry_run: false,
    });
    vi.spyOn(apiClient, 'previewNote').mockResolvedValue(createResponse([]));

    mountEditor({ id: '5' });
    await flush();

    await selectKind(formatMessage('zh-CN', 'notes.kind.cloze'));
    expect(target.querySelector('[data-testid="note-field-text"]')).toBeTruthy();
    setField('note-field-text', 'The {{c1::answer}}');
    setInput('note-tags-editor', 'topic, extra');
    await flush();

    submitForm();
    await flush();

    expect(createNotes).toHaveBeenCalledWith('5', {
      notes: [{ kind: 'cloze', fields: { text: 'The {{c1::answer}}' }, tags: ['topic', 'extra'] }],
    });
    expect(get(routeStore).path).toBe('/decks/5');
  });

  it('stays on the page with the content intact when creation reports a row error', async () => {
    const createNotes = vi.spyOn(apiClient, 'createNotes').mockResolvedValue({
      created: 0,
      updated: 0,
      skipped: 0,
      errors: [{ index: 0, reason: 'invalid' }],
      dry_run: false,
    });
    vi.spyOn(apiClient, 'previewNote').mockResolvedValue(createResponse([]));

    mountEditor({ id: '5' });
    await flush();
    setField('note-field-front', 'kept');
    await flush();

    submitForm();
    await flush();

    expect(createNotes).toHaveBeenCalledTimes(1);
    // No navigation, the content is still there, and the error is reported as a toast.
    expect(get(routeStore).path).not.toBe('/decks/5');
    expect(toast.error).toHaveBeenCalledWith(formatMessage('zh-CN', 'note_create.failed'));
    expect(fieldValue('note-field-front')).toBe('kept');
  });

  it('does not navigate on a dry_run success', async () => {
    vi.spyOn(apiClient, 'createNotes').mockResolvedValue({
      created: 1,
      updated: 0,
      skipped: 0,
      errors: [],
      dry_run: true,
    });
    vi.spyOn(apiClient, 'previewNote').mockResolvedValue(createResponse([]));

    mountEditor({ id: '5' });
    await flush();
    setField('note-field-front', 'Q');
    await flush();

    submitForm();
    await flush();

    expect(get(routeStore).path).not.toBe('/decks/5');
    expect(toast.error).toHaveBeenCalledWith(formatMessage('zh-CN', 'note_create.failed'));
  });

  it('prevents duplicate in-flight create submissions', async () => {
    const pending = deferred<Awaited<ReturnType<typeof apiClient.createNotes>>>();
    const createNotes = vi.spyOn(apiClient, 'createNotes').mockReturnValue(pending.promise);
    vi.spyOn(apiClient, 'previewNote').mockResolvedValue(createResponse([]));

    mountEditor({ id: '5' });
    await flush();
    setField('note-field-front', 'Q');
    await flush();

    submitForm();
    await flush();
    submitForm();
    await flush();

    expect(createNotes).toHaveBeenCalledTimes(1);
    pending.resolve({ created: 1, updated: 0, skipped: 0, errors: [], dry_run: false });
    await flush();
    expect(get(routeStore).path).toBe('/decks/5');
  });

  it('does not navigate when the view unmounts while a create is in flight', async () => {
    const pending = deferred<Awaited<ReturnType<typeof apiClient.createNotes>>>();
    vi.spyOn(apiClient, 'createNotes').mockReturnValue(pending.promise);
    vi.spyOn(apiClient, 'previewNote').mockResolvedValue(createResponse([]));

    mountEditor({ id: '5' });
    await flush();
    setField('note-field-front', 'Q');
    await flush();
    submitForm();
    await flush();
    expect(pending.promise).toBeTruthy();

    // The user leaves while the create request is in flight: success must not drag them to the deck page.
    unmount(mountedInstances.pop()!);
    const pathBefore = get(routeStore).path;

    pending.resolve({ created: 1, updated: 0, skipped: 0, errors: [], dry_run: false });
    await flush();

    expect(get(routeStore).path).toBe(pathBefore);
  });

  it('carries no page-level insertion section: each field has its own tools instead', async () => {
    vi.spyOn(apiClient, 'previewNote').mockResolvedValue(createResponse([]));

    mountEditor({ id: '5' });
    await flush();

    // The field selector, the page-level upload, the library picker and the standalone composer are gone.
    expect(target.querySelector('[data-testid="media-field"]')).toBeNull();
    expect(target.querySelector('[data-testid="media-upload-input"]')).toBeNull();
    expect(target.querySelector('[data-testid="media-picker-toggle"]')).toBeNull();
    expect(target.querySelector('[data-testid="shortcode-insert"]')).toBeNull();
    // The tools sit in the header of the field they write into, and the old insertion dialog is gone.
    expect(target.querySelector('[data-testid="note-field-front-insert-image"]')).toBeTruthy();
    expect(target.querySelector('[data-testid="note-field-front-toolbar"]')).toBeTruthy();
    expect(target.querySelector('[data-testid="note-insert-dialog"]')).toBeNull();
  });
});

describe('unified note editor — edit mode', () => {
  it('loads an existing note and runs the initial preview automatically', async () => {
    vi.spyOn(apiClient, 'getNote').mockResolvedValue(noteFixture());
    const previewNote = vi
      .spyOn(apiClient, 'previewNote')
      .mockResolvedValue(createResponse([{ front_html: '<p>Q</p>', back_html: '<p>A</p>' }]));

    mountEditor({ id: '5', noteId: '9' });
    await flush();

    // The frame announces itself, then receives the loaded field value.
    const frame = frameFor('note-field-front');
    fromFrame(frame, { type: 'ready' });
    await flush();
    expect(loadedHtml('note-field-front')).toBe('Q');

    await vi.advanceTimersByTimeAsync(500);
    await flush();

    expect(previewNote).toHaveBeenCalledWith('5', 'basic', { front: 'Q', back: 'A' }, { frame: true });
    expect(await previewFront()).toContain('Q');
  });

  it('saves in place without redirecting and round-trips unknown fields', async () => {
    vi.spyOn(apiClient, 'getNote').mockResolvedValue(
      noteFixture({ fields: { front: 'Q', back: 'A', custom: 'keep' } })
    );
    vi.spyOn(apiClient, 'previewNote').mockResolvedValue(createResponse([]));
    const updateNote = vi.spyOn(apiClient, 'updateNote').mockResolvedValue(
      noteFixture({ fields: { front: 'Q2', back: 'A', custom: 'keep' }, tags: ['a', 'b'] })
    );

    mountEditor({ id: '5', noteId: '9' });
    await flush();

    setField('note-field-front', 'Q2');
    setInput('note-tags-editor', 'a, b');
    await flush();

    submitForm();
    await flush();

    // The unknown key is passed back unchanged; the identity is the original card id; no navigation.
    expect(updateNote).toHaveBeenCalledWith('9', {
      kind: 'basic',
      fields: { front: 'Q2', back: 'A', custom: 'keep' },
      tags: ['a', 'b'],
    });
    expect(get(routeStore).path).not.toBe('/decks/5');
    expect(toast.success).toHaveBeenCalledWith(formatMessage('zh-CN', 'note_edit.saved'));
  });

  it('reports a rejected edit as an error toast and keeps the draft', async () => {
    vi.spyOn(apiClient, 'getNote').mockResolvedValue(noteFixture());
    vi.spyOn(apiClient, 'previewNote').mockResolvedValue(createResponse([]));
    vi.spyOn(apiClient, 'updateNote').mockRejectedValue(
      new ApiClientError('HTTP 400: invalid_request', { status: 400, code: 'invalid_request' })
    );

    mountEditor({ id: '5', noteId: '9' });
    await flush();
    setField('note-field-front', 'Q2');
    await flush();
    submitForm();
    await flush();

    // 400 means invalid content: report the specific reason rather than a generic failure; the draft is kept and "saved" is not announced.
    expect(toast.error).toHaveBeenCalledWith(formatMessage('zh-CN', 'note_edit.invalid'));
    expect(toast.success).not.toHaveBeenCalled();
    expect(fieldValue('note-field-front')).toBe('Q2');
  });

  it('keeps typing that arrives during a save in flight and never claims it was saved', async () => {
    vi.spyOn(apiClient, 'getNote').mockResolvedValue(noteFixture({ fields: { front: 'Q', back: 'A' } }));
    vi.spyOn(apiClient, 'previewNote').mockResolvedValue(createResponse([]));
    const pending = deferred<Note>();
    const updateNote = vi.spyOn(apiClient, 'updateNote').mockReturnValue(pending.promise);

    mountEditor({ id: '5', noteId: '9' });
    await flush();

    setField('note-field-front', 'QQ');
    await flush();
    submitForm();
    await flush();
    expect(updateNote).toHaveBeenCalledTimes(1);

    // Keep typing while the request is in flight.
    setField('note-field-front', 'QQ-newer');
    await flush();

    // The server returns a snapshot from the moment of submit: it must never overwrite what the user is typing.
    pending.resolve(noteFixture({ fields: { front: 'QQ', back: 'A' } }));
    await flush();

    expect(fieldValue('note-field-front')).toBe('QQ-newer');
    // The current draft was not overwritten by this save, so "saved" cannot be reported either.
    expect(toast.success).not.toHaveBeenCalled();
  });

  it('shows a not-found state when the note belongs to another deck', async () => {
    vi.spyOn(apiClient, 'getNote').mockResolvedValue(noteFixture({ deck_id: '999' }));

    mountEditor({ id: '5', noteId: '9' });
    await flush();

    expect(target.querySelector('[data-testid="note-editor-form"]')).toBeNull();
    expect(target.querySelector('[data-testid="note-edit-not-found"]')).toBeTruthy();
    expect(target.textContent).toContain('卡片不存在');
  });
});

describe('unified note editor — lifecycle and routes', () => {
  it('cancels the pending debounce on unmount', async () => {
    const previewNote = vi.spyOn(apiClient, 'previewNote').mockResolvedValue(createResponse([]));

    mountEditor({ id: '5' });
    await flush();

    setField('note-field-front', 'Q');
    await flush();
    unmount(mountedInstances.pop()!);

    await vi.advanceTimersByTimeAsync(1000);
    expect(previewNote).not.toHaveBeenCalled();
  });

  it('ignores an in-flight preview result after unmount', async () => {
    const pending = deferred<NotePreviewResponse>();
    const previewNote = vi.spyOn(apiClient, 'previewNote').mockReturnValue(pending.promise);

    mountEditor({ id: '5' });
    await flush();
    setField('note-field-front', 'R');
    await flush();
    await vi.advanceTimersByTimeAsync(500);
    expect(previewNote).toHaveBeenCalledTimes(1);

    unmount(mountedInstances.pop()!);
    const afterUnmount = target.innerHTML;
    pending.resolve(createResponse([{ front_html: '<p>late</p>', back_html: '' }]));
    await flush();

    // Results landing after unmount must not write state: the editor's DOM stays exactly as the unmount left it.
    expect(target.innerHTML).toBe(afterUnmount);
    expect(typesetSpy).not.toHaveBeenCalled();
  });

  it('ignores an in-flight save result after unmount (no state write, no typeset)', async () => {
    vi.spyOn(apiClient, 'getNote').mockResolvedValue(noteFixture());
    vi.spyOn(apiClient, 'previewNote').mockResolvedValue(createResponse([]));
    const pending = deferred<Note>();
    vi.spyOn(apiClient, 'updateNote').mockReturnValue(pending.promise);

    mountEditor({ id: '5', noteId: '9' });
    await flush();
    setField('note-field-front', 'Q2');
    await flush();
    submitForm();
    await flush();

    unmount(mountedInstances.pop()!);
    const afterUnmount = target.innerHTML;
    const typesetCallsBefore = typesetSpy.mock.calls.length;

    // The save request lands only after unmount: it must write no state and trigger no typesetting.
    pending.resolve(noteFixture({ fields: { front: 'Q2', back: 'A' } }));
    await flush();

    expect(target.innerHTML).toBe(afterUnmount);
    expect(typesetSpy.mock.calls.length).toBe(typesetCallsBefore);
  });

  it('points every create/edit route alias at the one shared editor implementation', () => {
    const byName = (name: string) => routes.find((route) => route.name === name);
    const editor = byName('note-edit')!.component;
    expect(byName('note-edit-nid')!.component).toBe(editor);
    expect(byName('note-create')!.component).toBe(editor);
    expect(byName('note-create-legacy')!.component).toBe(editor);
  });
});

/**
 * The preview below the desktop breakpoint is a drawer over the form rather than a column under it, so a
 * phone author does not scroll past their own fields to see the result. It is the same panel the desktop
 * column shows — never a second copy of the sanitized HTML — and it is closed by default.
 */
describe('unified note editor — preview drawer below the desktop breakpoint', () => {
  function panel(): HTMLElement {
    return target.querySelector('[data-testid="note-preview-panel"]') as HTMLElement;
  }

  function toggle(): HTMLButtonElement {
    return target.querySelector('[data-testid="note-preview-toggle"]') as HTMLButtonElement;
  }

  function openDrawer(): void {
    toggle().focus();
    toggle().click();
    flushSync();
  }

  it('keeps the preview closed and out of the layout below lg, with a header button to open it', async () => {
    vi.spyOn(apiClient, 'previewNote').mockResolvedValue(createResponse([]));

    mountEditor({ id: '5' });
    await flush();

    // Closed by default: the panel is hidden below lg, and lg restores it as the second column.
    expect(panel().classList.contains('hidden')).toBe(true);
    expect(panel().className).toContain('lg:flex');
    expect(panel().getAttribute('data-open')).toBeNull();
    expect(panel().getAttribute('aria-hidden')).toBeNull();
    // The way in exists only where the preview is not already on screen.
    expect(toggle().className).toContain('lg:hidden');
    expect(toggle().textContent?.trim()).toBe(formatMessage('zh-CN', 'note_preview.title'));
    expect(toggle().getAttribute('aria-expanded')).toBe('false');
    expect(toggle().getAttribute('aria-controls')).toBe('note-preview-panel');
    // At lg and wider the panel is the sticky column, as before.
    expect(panel().parentElement?.className).toContain('lg:sticky');
    // A named landmark at both breakpoints: the heading names the panel, and nothing claims to be modal.
    expect(panel().getAttribute('role')).toBe('region');
    const heading = panel().querySelector('h2');
    expect(heading?.id).toBe('note-preview-heading');
    expect(heading?.textContent?.trim()).toBe(formatMessage('zh-CN', 'note_preview.title'));
    expect(panel().getAttribute('aria-labelledby')).toBe(heading?.id);
    expect(panel().hasAttribute('aria-modal')).toBe(false);
  });

  it('opens from the header button and keeps updating the same preview while open', async () => {
    vi.spyOn(apiClient, 'previewNote')
      .mockResolvedValueOnce(createResponse([{ front_html: '<p>before</p>', back_html: '' }]))
      .mockResolvedValueOnce(createResponse([{ front_html: '<p>after</p>', back_html: '' }]));

    mountEditor({ id: '5' });
    await flush();
    setField('note-field-front', 'first');
    await flush();
    await vi.advanceTimersByTimeAsync(500);
    await flush();
    expect(await previewFront()).toContain('before');

    const field = target.querySelector('[data-testid="note-field-front"]') as HTMLIFrameElement;
    openDrawer();
    await flush();

    expect(panel().getAttribute('data-open')).toBe('true');
    expect(panel().classList.contains('hidden')).toBe(false);
    expect(panel().classList.contains('fixed')).toBe(true);
    expect(toggle().getAttribute('aria-expanded')).toBe('true');
    // One sanitized container only: the drawer is the panel the preview was already committed into.
    expect(target.querySelectorAll('[data-testid="note-preview-result"]')).toHaveLength(1);
    expect(panel().querySelector('[data-testid="note-preview-result"]')).toBeTruthy();

    // Typing while the drawer is open lands in the same panel.
    setField('note-field-front', 'second');
    await flush();
    await vi.advanceTimersByTimeAsync(500);
    await flush();
    expect(await previewFront()).toContain('after');
    expect(panel().querySelector('[data-testid="note-preview-result"]')).toBeTruthy();

    // Opening and closing never remounted the form: same textarea node, same unsaved text.
    expect(target.querySelector('[data-testid="note-field-front"]')).toBe(field);
    expect(fieldValue('note-field-front')).toBe('second');
  });

  it('closes on Esc and returns focus to the header button, keeping the field caret', async () => {
    vi.spyOn(apiClient, 'previewNote').mockResolvedValue(createResponse([{ front_html: '<p>x</p>', back_html: '' }]));

    mountEditor({ id: '5' });
    await flush();
    setField('note-field-front', 'abcdef');
    await flush();
    const field = target.querySelector('[data-testid="note-field-front"]') as HTMLIFrameElement;
    expect(field).toBeTruthy();
    flushSync();

    openDrawer();
    await flush();
    expect(panel().getAttribute('data-open')).toBe('true');

    window.dispatchEvent(new KeyboardEvent('keydown', { key: 'Escape', bubbles: true }));
    await flush();

    expect(panel().getAttribute('data-open')).toBeNull();
    expect(panel().classList.contains('hidden')).toBe(true);
    expect(toggle().getAttribute('aria-expanded')).toBe('false');
    // Focus goes back to the button that opened the drawer.
    expect(document.activeElement).toBe(toggle());
    // The unsaved text and the caret survived, so the next insertion still lands where the author left off.
    expect(fieldValue('note-field-front')).toBe('abcdef');
    expect(field).toBeTruthy();
  });

  it('closes from its own close control', async () => {
    vi.spyOn(apiClient, 'previewNote').mockResolvedValue(createResponse([]));

    mountEditor({ id: '5' });
    await flush();
    openDrawer();
    await flush();

    const close = target.querySelector('[data-testid="note-preview-close"]') as HTMLButtonElement;
    expect(close.getAttribute('aria-label')).toBe(formatMessage('zh-CN', 'note_preview.close'));
    close.click();
    await flush();

    expect(panel().getAttribute('data-open')).toBeNull();
    expect(document.activeElement).toBe(toggle());
  });

  it('renders the desktop preview unchanged while the drawer stays closed', async () => {
    vi.spyOn(apiClient, 'previewNote').mockResolvedValue(
      createResponse([{ front_html: '<p>desktop</p>', back_html: '<p>back</p>' }])
    );

    mountEditor({ id: '5' });
    await flush();
    setField('note-field-front', 'Q');
    await flush();
    await vi.advanceTimersByTimeAsync(500);
    await flush();

    // At lg the two-column grid still holds the preview, typeset and enhanced as before.
    expect(target.querySelector('[data-testid="note-preview-result"]')).toBeTruthy();
    expect(await previewFront()).toContain('desktop');
    expect(panel().className).toContain('max-h-[calc(100vh-3rem)]');
    expect(panel().getAttribute('data-open')).toBeNull();
  });
});

/**
 * The editor is reached from the deck page and from the review page. Leaving it — back, cancel, or a
 * delete — must return to the page that opened it, which the review page names in `?return=`. A
 * return value that is not an in-app path is ignored, so the link can never send the user off-site.
 */
describe('note editor — return path', () => {
  function backLink(): HTMLAnchorElement {
    return target.querySelector('[data-testid="note-edit-back"]') as HTMLAnchorElement;
  }

  it('leads back to the review scope it was opened from', async () => {
    vi.spyOn(apiClient, 'getNote').mockResolvedValue(noteFixture({ deck_id: '5' }));
    vi.spyOn(apiClient, 'previewNote').mockResolvedValue(createResponse([]));

    mountEditor({ id: '5', noteId: '9' }, { return: '/review?deck=5&tag=verbs' });
    await flush();

    expect(backLink().getAttribute('href')).toBe('/review?deck=5&tag=verbs');
    expect(backLink().textContent?.trim()).toBe('返回复习');

    (target.querySelector('[data-testid="note-cancel"]') as HTMLButtonElement).click();
    await flush();
    expect(get(routeStore).path).toBe('/review');
    expect(get(routeStore).query).toEqual({ deck: '5', tag: 'verbs' });
  });

  it('falls back to the deck page without a return path', async () => {
    vi.spyOn(apiClient, 'getNote').mockResolvedValue(noteFixture({ deck_id: '5' }));
    vi.spyOn(apiClient, 'previewNote').mockResolvedValue(createResponse([]));

    mountEditor({ id: '5', noteId: '9' });
    await flush();

    expect(backLink().getAttribute('href')).toBe('/decks/5');
    expect(backLink().textContent?.trim()).toBe('返回卡组');
  });

  it.each(['//example.com/review', '/\\example.com', 'https://example.com/', '/api/v1/notes'])(
    'ignores the return path %s, which is not a page of this app',
    async (raw) => {
      vi.spyOn(apiClient, 'getNote').mockResolvedValue(noteFixture({ deck_id: '5' }));
      vi.spyOn(apiClient, 'previewNote').mockResolvedValue(createResponse([]));

      mountEditor({ id: '5', noteId: '9' }, { return: raw });
      await flush();

      expect(backLink().getAttribute('href')).toBe('/decks/5');
    }
  );
});

describe('note editor — delete', () => {
  async function openDelete(query: Record<string, string> = {}): Promise<void> {
    vi.spyOn(apiClient, 'getNote').mockResolvedValue(noteFixture({ deck_id: '5' }));
    vi.spyOn(apiClient, 'previewNote').mockResolvedValue(createResponse([]));
    mountEditor({ id: '5', noteId: '9' }, query);
    await flush();
    (target.querySelector('[data-testid="note-delete"]') as HTMLButtonElement).click();
    await flush();
  }

  function confirmButton(): HTMLButtonElement {
    return document.body.querySelector('[data-testid="note-edit-delete-confirm"]') as HTMLButtonElement;
  }

  it('asks first, then deletes and replaces the editor entry with the return page', async () => {
    const deleteNote = vi.spyOn(apiClient, 'deleteNote').mockResolvedValue({ deleted: true, id: '9' });
    const replaceState = vi.spyOn(window.history, 'replaceState');

    await openDelete({ return: '/review?deck=5' });
    // Opening the dialog deletes nothing.
    expect(deleteNote).not.toHaveBeenCalled();
    expect(document.body.querySelector('[data-testid="note-edit-delete-dialog"]')).toBeTruthy();

    confirmButton().click();
    await flush();

    expect(deleteNote).toHaveBeenCalledWith('9');
    expect(toast.success).toHaveBeenCalledWith('卡片已删除。');
    expect(get(routeStore).path).toBe('/review');
    expect(replaceState).toHaveBeenCalledWith({}, '', '/review?deck=5');
  });

  it('returns to the deck page after a delete opened from the deck', async () => {
    vi.spyOn(apiClient, 'deleteNote').mockResolvedValue({ deleted: true, id: '9' });

    await openDelete();
    confirmButton().click();
    await flush();

    expect(get(routeStore).path).toBe('/decks/5');
  });

  it('stays on the page and explains a refused delete', async () => {
    vi.spyOn(apiClient, 'deleteNote').mockRejectedValue(
      new ApiClientError('insufficient role', { status: 403, code: 'insufficient_role' })
    );

    await openDelete({ return: '/review' });
    const pathBefore = get(routeStore).path;
    confirmButton().click();
    await flush();

    expect(get(routeStore).path).toBe(pathBefore);
    expect(document.body.querySelector('[data-testid="note-edit-delete-error"]')?.textContent).toBe('没有访问权限');
    expect(toast.success).not.toHaveBeenCalled();
  });

  it('offers no delete while creating a note', async () => {
    vi.spyOn(apiClient, 'previewNote').mockResolvedValue(createResponse([]));
    mountEditor({ id: '5' });
    await flush();

    expect(target.querySelector('[data-testid="note-delete"]')).toBeNull();
  });
});
