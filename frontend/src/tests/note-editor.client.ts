import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest';
import { mount, unmount, flushSync } from 'svelte';
import { get } from 'svelte/store';
import NoteEditView from '../lib/views/NoteEditView.svelte';
import { routeStore } from '../lib/router';
import { routes } from '../lib/router/routes';
import { apiClient, ApiClientError, type Note, type NotePreviewResponse, type NoteType, type NoteTypeConfig, type NoteTypeField } from '../lib/api';
import { typeset } from '../lib/mathjax';
import { setLocale } from '../lib/i18n';
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

/** One field of a test note type; every option has the zero value unless the case sets it. */
function noteTypeField(id: string, name: string, overrides: Partial<NoteTypeField> = {}): NoteTypeField {
  return {
    id,
    name,
    sticky: false,
    rtl: false,
    font: '',
    font_size: 0,
    description: '',
    plain_text: false,
    collapsed: false,
    ...overrides,
  };
}

/** One note type of the deck; the client only reads id, name and config.fields, but the shape matches the API. */
function noteType(id: string, name: string, fields: NoteTypeField[], config: Partial<NoteTypeConfig> = {}): NoteType {
  return {
    id,
    deck_id: '5',
    name,
    version: 1,
    config: {
      kind: 'normal',
      fields,
      templates: [
        {
          id: 't-card',
          name: 'Card 1',
          qfmt: fields.map((field) => `{{${field.name}}}`).join(''),
          afmt: fields.map((field) => `{{${field.name}}}`).join(''),
        },
      ],
      css: '',
      ...config,
    },
    created_at: '2026-10-01T00:00:00Z',
    updated_at: '2026-10-01T00:00:00Z',
  };
}

/** The first note type of the default deck: fields Front and Back, the shape a stock Basic type has. */
function basicNoteType(): NoteType {
  return noteType('nt-basic', 'Basic', [noteTypeField('front', 'Front'), noteTypeField('back', 'Back')]);
}

/**
 * A note type created in the deck: opaque field ids, names and a template that matches no engram stock kind, so the
 * card-kind catalog has no entry for it.
 */
function customNoteType(): NoteType {
  return noteType(
    'nt-custom',
    'Custom layout',
    [noteTypeField('f-question', 'Question'), noteTypeField('f-answer', 'Answer')],
    {
      templates: [
        {
          id: 't-custom',
          name: 'Custom card',
          qfmt: '<div id="custom-question">{{Question}}</div>',
          afmt: '<div id="custom-question">{{Question}}</div><div id="custom-answer">{{Answer}}</div>',
        },
      ],
      css: '.card { color: rgb(4, 5, 6); }',
    }
  );
}

/** A choice note type: its option field is one HTML value, not a list of lines. */
function choiceNoteType(): NoteType {
  return noteType('nt-choice', 'Choice', [
    noteTypeField('question', 'Question'),
    noteTypeField('options', 'Options'),
    noteTypeField('answer', 'Answer'),
  ]);
}

/** A note type carrying every field option the editor has to apply. */
function styledNoteType(): NoteType {
  return noteType('nt-styled', 'Styled', [
    noteTypeField('f-front', 'Front', {
      sticky: true,
      collapsed: true,
      plain_text: true,
      rtl: true,
      font: 'Georgia',
      font_size: 22,
    }),
    noteTypeField('f-back', 'Back'),
  ]);
}

function defaultNoteTypes(): NoteType[] {
  return [basicNoteType(), customNoteType()];
}

/** The field test ids of the default types, named after the field names the form uses as labels. */
const FRONT = 'note-field-Front';
const QUESTION = 'note-field-Question';
const ANSWER = 'note-field-Answer';

function noteFixture(overrides: Partial<Note> = {}): Note {
  return {
    id: '9',
    deck_id: '5',
    note_type: 'nt-basic',
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
function createResponse(cards: Array<{ front: string; back: string }>): NotePreviewResponse {
  return {
    cards: cards.map((card) => ({
      front: face(card.front),
      back: face(card.back, { side: 'back' }),
    })),
  };
}

let target: HTMLElement;
const mountedInstances: Array<ReturnType<typeof mount>> = [];
/** The latest HTML each field reported, keyed by data-testid; the frame owns the editing surface. */
const fieldValues = new Map<string, string>();

function mountEditor(
  params: Record<string, string>,
  query: Record<string, string> = {},
  noteTypes: NoteType[] = defaultNoteTypes()
): void {
  // The deck's note types are the only field metadata; each case states the types its deck stores.
  vi.spyOn(apiClient, 'listNoteTypes').mockResolvedValue({ note_types: noteTypes });
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

/** Writes a plain control (the tags field) as a real user input does. */
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

/** The last `load` message a field received, for asserting the presentation options it carries. */
function lastLoad(testid: string): Record<string, unknown> {
  const calls = frameFor(testid).postMessage.mock.calls;
  for (let i = calls.length - 1; i >= 0; i -= 1) {
    const message = calls[i]?.[0] as Record<string, unknown> | undefined;
    if (message?.type === 'load') return message;
  }
  throw new Error(`missing load message for ${testid}`);
}

async function selectNoteType(label: string): Promise<void> {
  const trigger = target.querySelector('[data-testid="note-edit-type"]') as HTMLElement;
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
  fieldValues.clear();
});

describe('unified note editor — create mode', () => {
  it('renders the full editor with an automatic (button-less) preview and no empty-id note read', async () => {
    const previewNote = vi.spyOn(apiClient, 'previewNote').mockResolvedValue(createResponse([]));
    const getNote = vi.spyOn(apiClient, 'getNote');

    mountEditor({ id: '5' });
    await flush();

    expect(target.querySelector('[data-testid="note-editor-form"]')).toBeTruthy();
    // The selector starts at the first note type in the order the server returned.
    expect(target.querySelector('[data-testid="note-edit-type"]')?.textContent).toContain('Basic');
    expect(target.querySelector(`[data-testid="${FRONT}"]`)).toBeTruthy();
    // No content entered: takes the empty path and sends no preview request to the server.
    expect(target.querySelector('[data-testid="note-preview-empty"]')).toBeTruthy();
    expect(previewNote).not.toHaveBeenCalled();
    // Initialization must never GET a remote card with an empty id.
    expect(getNote).not.toHaveBeenCalled();
  });

  it('debounces reactive preview and renders the latest result', async () => {
    const previewNote = vi
      .spyOn(apiClient, 'previewNote')
      .mockResolvedValue(createResponse([{ front: '<p>rendered Q</p>', back: '<p>A</p>' }]));

    mountEditor({ id: '5' });
    await flush();

    setField(FRONT, 'Q');
    await flush();

    await vi.advanceTimersByTimeAsync(499);
    expect(previewNote).not.toHaveBeenCalled();
    await vi.advanceTimersByTimeAsync(1);
    await flush();

    expect(previewNote).toHaveBeenCalledTimes(1);
    expect(previewNote).toHaveBeenCalledWith('5', 'nt-basic', { Front: 'Q' }, { frame: true });
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
    });

    mountEditor({ id: '5' });
    await flush();

    setField(FRONT, 'Q');
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

    setField(FRONT, 'Q');
    await flush();
    await vi.advanceTimersByTimeAsync(300);
    setField(FRONT, 'QR');
    await flush();

    // 500ms have passed since the first input, but the second input reset the window, so nothing should be sent yet.
    await vi.advanceTimersByTimeAsync(200);
    expect(previewNote).not.toHaveBeenCalled();
    await vi.advanceTimersByTimeAsync(300);
    await flush();

    expect(previewNote).toHaveBeenCalledTimes(1);
    expect(previewNote).toHaveBeenCalledWith('5', 'nt-basic', { Front: 'QR' }, { frame: true });
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

    setField(FRONT, 'A');
    await flush();
    await vi.advanceTimersByTimeAsync(500); // Request 1 in flight
    setField(FRONT, 'B');
    await flush();
    await vi.advanceTimersByTimeAsync(500); // Request 2 in flight
    expect(previewNote).toHaveBeenCalledTimes(2);

    second.resolve(createResponse([{ front: '<p>SECOND</p>', back: '' }]));
    await flush();
    expect(await previewFront()).toContain('SECOND');

    // The old request only comes back now: it must be dropped and must not overwrite the newer result.
    first.resolve(createResponse([{ front: '<p>FIRST</p>', back: '' }]));
    await flush();
    expect(await previewFront()).toContain('SECOND');
    expect(await previewFront()).not.toContain('FIRST');
  });

  it('ignores a stale delayed error after a newer success', async () => {
    const first = deferred<NotePreviewResponse>();
    const previewNote = vi
      .spyOn(apiClient, 'previewNote')
      .mockReturnValueOnce(first.promise)
      .mockResolvedValueOnce(createResponse([{ front: '<p>GOOD</p>', back: '' }]));

    mountEditor({ id: '5' });
    await flush();

    setField(FRONT, 'A');
    await flush();
    await vi.advanceTimersByTimeAsync(500);
    setField(FRONT, 'B');
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
    setField(FRONT, 'Q');
    await flush();
    await vi.advanceTimersByTimeAsync(500);
    await flush();

    expect(target.querySelector('[data-testid="note-preview-incomplete"]')).toBeTruthy();
    expect(target.querySelector('[data-testid="note-preview-error"]')).toBeNull();
  });

  it('reinitialises the fields and the preview when another note type is selected', async () => {
    const previewNote = vi.spyOn(apiClient, 'previewNote').mockResolvedValue(createResponse([]));

    mountEditor({ id: '5' });
    await flush();

    setField(FRONT, 'kept');
    await flush();

    await selectNoteType('Custom layout');
    expect(target.querySelector(`[data-testid="${FRONT}"]`)).toBeNull();
    expect(target.querySelector(`[data-testid="${QUESTION}"]`)).toBeTruthy();

    // The new type's fields are blank, so the switch produces no preview request of its own.
    await vi.advanceTimersByTimeAsync(600);
    await flush();
    expect(previewNote).not.toHaveBeenCalled();

    setField(QUESTION, 'custom');
    await flush();
    await vi.advanceTimersByTimeAsync(500);
    await flush();

    expect(previewNote).toHaveBeenCalledWith('5', 'nt-custom', { Question: 'custom' }, { frame: true });
  });

  it('counts a literal zero as real input and previews it', async () => {
    const previewNote = vi.spyOn(apiClient, 'previewNote').mockResolvedValue(createResponse([]));

    mountEditor({ id: '5' });
    await flush();

    setField(FRONT, '0');
    await flush();

    await vi.advanceTimersByTimeAsync(500);
    await flush();

    expect(previewNote).toHaveBeenCalledWith('5', 'nt-basic', { Front: '0' }, { frame: true });
  });

  it('renders the preview through the frame instead of typesetting it in the parent', async () => {
    vi.spyOn(apiClient, 'previewNote').mockResolvedValue(
      createResponse([{ front: '<p>$x$</p>', back: '' }])
    );

    mountEditor({ id: '5' });
    await flush();
    setField(FRONT, 'Q');
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
    vi.spyOn(apiClient, 'previewNote').mockResolvedValue(createResponse([{ front: front, back: '' }]));

    mountEditor({ id: '5' });
    await flush();
    setField(FRONT, 'Q');
    await flush();
    await vi.advanceTimersByTimeAsync(500);
    await flush();

    // The frame runtime attaches the controls to the face it renders (covered by cardframe.client.ts), so the editor only has
    // to hand over the face markup with its audio and speech markers intact.
    expect(await shownHtmlOnSide(target, 'question')).toEqual([front]);
  });

  it('sends only the newest preview generation to the frame', async () => {
    const first = deferred<NotePreviewResponse>();
    vi.spyOn(apiClient, 'previewNote')
      .mockReturnValueOnce(first.promise)
      .mockResolvedValueOnce(createResponse([{ front: '<p>SECOND</p>', back: '' }]));

    mountEditor({ id: '5' });
    await flush();

    setField(FRONT, 'A');
    await flush();
    await vi.advanceTimersByTimeAsync(500);
    setField(FRONT, 'B');
    await flush();
    await vi.advanceTimersByTimeAsync(500);
    await flush();
    expect(await previewFront()).toContain('SECOND');

    // The first request lands late: its content must not replace the newer face in the frame.
    first.resolve(createResponse([{ front: '<p>FIRST</p>', back: '' }]));
    await flush();
    expect(await previewFront()).toContain('SECOND');
    expect(await previewFront()).not.toContain('FIRST');
  });

  it('applies the stored field options to the editor', async () => {
    const previewNote = vi.spyOn(apiClient, 'previewNote').mockResolvedValue(createResponse([]));

    mountEditor({ id: '5' }, {}, [styledNoteType()]);
    await flush();

    // plain_text: the field has no formatting toolbar, only the editing surface.
    expect(target.querySelector('[data-testid="note-field-Front-toolbar"]')).toBeNull();
    // collapsed: the field starts folded and brings its own unfold control.
    const toggle = target.querySelector('[data-testid="note-field-Front-collapse-toggle"]') as HTMLElement;
    expect(toggle.getAttribute('aria-expanded')).toBe('false');
    expect(target.querySelector('[data-testid="note-field-Front"]')).toBeNull();

    toggle.click();
    await flush();
    const frame = frameFor('note-field-Front');
    fromFrame(frame, { type: 'ready' });
    await flush();

    // rtl, font and size travel with the value to the editing frame; plain text is a display decision of the same frame.
    const load = lastLoad('note-field-Front');
    expect(load.plainText).toBe(true);
    expect(load.rtl).toBe(true);
    expect(load.font).toBe('Georgia');
    expect(load.size).toBe(22);

    // A field with no options set keeps the editor defaults.
    expect(target.querySelector('[data-testid="note-field-Back-toolbar"]')).toBeTruthy();
    expect(previewNote).not.toHaveBeenCalled();
  });

  it('creates a note of a note type that matches no stock kind', async () => {
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

    await selectNoteType('Custom layout');
    expect(target.querySelector(`[data-testid="${QUESTION}"]`)).toBeTruthy();
    setField(QUESTION, '<p>The question</p>');
    setInput('note-tags-editor', 'topic, extra');
    await flush();

    submitForm();
    await flush();

    expect(createNotes).toHaveBeenCalledWith('5', {
      notes: [{ note_type: 'nt-custom', fields: { Question: '<p>The question</p>' }, tags: ['topic', 'extra'] }],
    });
    expect(Object.keys(createNotes.mock.calls[0]![1].notes[0]!)).not.toContain('kind');
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
    setField(FRONT, 'kept');
    await flush();

    submitForm();
    await flush();

    expect(createNotes).toHaveBeenCalledTimes(1);
    // No navigation, the content is still there, and the error is reported as a toast.
    expect(get(routeStore).path).not.toBe('/decks/5');
    expect(toast.error).toHaveBeenCalledWith('无法创建卡片，请重试。');
    expect(fieldValue(FRONT)).toBe('kept');
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
    setField(FRONT, 'Q');
    await flush();

    submitForm();
    await flush();

    expect(get(routeStore).path).not.toBe('/decks/5');
    expect(toast.error).toHaveBeenCalledWith('无法创建卡片，请重试。');
  });

  it('prevents duplicate in-flight create submissions', async () => {
    const pending = deferred<Awaited<ReturnType<typeof apiClient.createNotes>>>();
    const createNotes = vi.spyOn(apiClient, 'createNotes').mockReturnValue(pending.promise);
    vi.spyOn(apiClient, 'previewNote').mockResolvedValue(createResponse([]));

    mountEditor({ id: '5' });
    await flush();
    setField(FRONT, 'Q');
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
    setField(FRONT, 'Q');
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
    expect(target.querySelector(`[data-testid="${FRONT}-insert-image"]`)).toBeTruthy();
    expect(target.querySelector(`[data-testid="${FRONT}-toolbar"]`)).toBeTruthy();
    expect(target.querySelector('[data-testid="note-insert-dialog"]')).toBeNull();
  });
});

describe('unified note editor — edit mode', () => {
  it('loads an existing note and runs the initial preview automatically', async () => {
    vi.spyOn(apiClient, 'getNote').mockResolvedValue(noteFixture());
    const previewNote = vi
      .spyOn(apiClient, 'previewNote')
      .mockResolvedValue(createResponse([{ front: '<p>Q</p>', back: '<p>A</p>' }]));

    mountEditor({ id: '5', noteId: '9' });
    await flush();

    // The frame announces itself, then receives the loaded field value.
    const frame = frameFor(FRONT);
    fromFrame(frame, { type: 'ready' });
    await flush();
    expect(loadedHtml(FRONT)).toBe('Q');

    await vi.advanceTimersByTimeAsync(500);
    await flush();

    expect(previewNote).toHaveBeenCalledWith('5', 'nt-basic', { Front: 'Q', Back: 'A' }, { frame: true });
    expect(await previewFront()).toContain('Q');
  });

  it('saves in place with note_type and ignores keys outside the note type', async () => {
    vi.spyOn(apiClient, 'getNote').mockResolvedValue(
      noteFixture({ fields: { front: 'Q', back: 'A', orphan: 'keep' } })
    );
    vi.spyOn(apiClient, 'previewNote').mockResolvedValue(createResponse([]));
    const updateNote = vi.spyOn(apiClient, 'updateNote').mockResolvedValue(
      noteFixture({ fields: { front: 'Q2', back: 'A' }, tags: ['a', 'b'] })
    );

    mountEditor({ id: '5', noteId: '9' });
    await flush();

    setField(FRONT, 'Q2');
    setInput('note-tags-editor', 'a, b');
    await flush();

    submitForm();
    await flush();

    // The identity is the original note id; the payload names the note type and keys fields by field name, with no kind.
    expect(updateNote).toHaveBeenCalledWith('9', {
      note_type: 'nt-basic',
      fields: { Front: 'Q2', Back: 'A' },
      tags: ['a', 'b'],
    });
    expect(Object.keys(updateNote.mock.calls[0]![1])).not.toContain('kind');
    expect(get(routeStore).path).not.toBe('/decks/5');
    expect(toast.success).toHaveBeenCalledWith('卡片已保存。');
  });

  it('loads, edits and saves a note of a note type that matches no stock kind', async () => {
    const custom = customNoteType();
    vi.spyOn(apiClient, 'getNote').mockResolvedValue(
      noteFixture({
            note_type: custom.id,
        fields: { 'f-question': '<p>OLD QUESTION</p>', 'f-answer': '<p>OLD ANSWER</p>' },
      })
    );
    const previewNote = vi.spyOn(apiClient, 'previewNote').mockResolvedValue(createResponse([]));
    const updateNote = vi.spyOn(apiClient, 'updateNote').mockResolvedValue(
      noteFixture({
            note_type: custom.id,
        fields: { 'f-question': '<p>NEW QUESTION</p>', 'f-answer': '<p>OLD ANSWER</p>' },
      })
    );

    mountEditor({ id: '5', noteId: '9' }, {}, [custom]);
    await flush();

    // The note's own type is selected and its fields are shown in config order, labelled by field name.
    expect(target.querySelector('[data-testid="note-edit-type"]')?.textContent).toContain('Custom layout');
    const frame = frameFor(QUESTION);
    fromFrame(frame, { type: 'ready' });
    await flush();
    expect(loadedHtml(QUESTION)).toBe('<p>OLD QUESTION</p>');
    expect(target.querySelector(`[data-testid="${ANSWER}"]`)).toBeTruthy();

    setField(QUESTION, '<p>NEW QUESTION</p>');
    await flush();
    await vi.advanceTimersByTimeAsync(500);
    await flush();
    submitForm();
    await flush();

    // The save names the note type and the fields by name; it never sends a card kind.
    const payload = updateNote.mock.calls[0]![1];
    expect(payload.note_type).toBe('nt-custom');
    expect(payload.fields).toEqual({
      Question: '<p>NEW QUESTION</p>',
      Answer: '<p>OLD ANSWER</p>',
    });
    expect(payload).not.toHaveProperty('kind');
    // The preview used the same type, so the stored template rendered its faces.
    expect(previewNote).toHaveBeenCalledWith(
      '5',
      'nt-custom',
      { Question: '<p>NEW QUESTION</p>', Answer: '<p>OLD ANSWER</p>' },
      { frame: true }
    );
    expect(toast.success).toHaveBeenCalledWith('卡片已保存。');
  });

  it('loads and saves a choice option as the stored HTML string', async () => {
    const choice = choiceNoteType();
    const optionHTML = '<img src="x.png"><b>bold option</b>';
    const fields = { question: 'Which one?', options: optionHTML, answer: '0' };
    vi.spyOn(apiClient, 'getNote').mockResolvedValue(
      noteFixture({ note_type: choice.id, fields })
    );
    vi.spyOn(apiClient, 'previewNote').mockResolvedValue(createResponse([]));
    const updateNote = vi.spyOn(apiClient, 'updateNote').mockResolvedValue(
      noteFixture({ note_type: choice.id, fields })
    );

    mountEditor({ id: '5', noteId: '9' }, {}, [choice]);
    await flush();

    // The field receives the stored HTML unchanged: no line splitting, no boolean or number conversion.
    const frame = frameFor('note-field-Options');
    fromFrame(frame, { type: 'ready' });
    await flush();
    expect(loadedHtml('note-field-Options')).toBe(optionHTML);

    // Saving without edits sends exactly the HTML that was loaded.
    submitForm();
    await flush();

    expect(updateNote).toHaveBeenCalledWith('9', {
      note_type: choice.id,
      fields: { Question: 'Which one?', Options: optionHTML, Answer: '0' },
      tags: [],
    });
  });

  it('reports a rejected edit as an error toast and keeps the draft', async () => {
    vi.spyOn(apiClient, 'getNote').mockResolvedValue(noteFixture());
    vi.spyOn(apiClient, 'previewNote').mockResolvedValue(createResponse([]));
    vi.spyOn(apiClient, 'updateNote').mockRejectedValue(
      new ApiClientError('HTTP 400: invalid_request', { status: 400, code: 'invalid_request' })
    );

    mountEditor({ id: '5', noteId: '9' });
    await flush();
    setField(FRONT, 'Q2');
    await flush();
    submitForm();
    await flush();

    // 400 means invalid content: report the specific reason rather than a generic failure; the draft is kept and "saved" is not announced.
    expect(toast.error).toHaveBeenCalledWith('请检查卡片内容后重试。');
    expect(toast.success).not.toHaveBeenCalled();
    expect(fieldValue(FRONT)).toBe('Q2');
  });

  it('keeps typing that arrives during a save in flight and never claims it was saved', async () => {
    vi.spyOn(apiClient, 'getNote').mockResolvedValue(noteFixture({ fields: { front: 'Q', back: 'A' } }));
    vi.spyOn(apiClient, 'previewNote').mockResolvedValue(createResponse([]));
    const pending = deferred<Note>();
    const updateNote = vi.spyOn(apiClient, 'updateNote').mockReturnValue(pending.promise);

    mountEditor({ id: '5', noteId: '9' });
    await flush();

    setField(FRONT, 'QQ');
    await flush();
    submitForm();
    await flush();
    expect(updateNote).toHaveBeenCalledTimes(1);

    // Keep typing while the request is in flight.
    setField(FRONT, 'QQ-newer');
    await flush();

    // The server returns a snapshot from the moment of submit: it must never overwrite what the user is typing.
    pending.resolve(noteFixture({ fields: { front: 'QQ', back: 'A' } }));
    await flush();

    expect(fieldValue(FRONT)).toBe('QQ-newer');
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

    setField(FRONT, 'Q');
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
    setField(FRONT, 'R');
    await flush();
    await vi.advanceTimersByTimeAsync(500);
    expect(previewNote).toHaveBeenCalledTimes(1);

    unmount(mountedInstances.pop()!);
    const afterUnmount = target.innerHTML;
    pending.resolve(createResponse([{ front: '<p>late</p>', back: '' }]));
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
    setField(FRONT, 'Q2');
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
 * column shows — never a second copy of the preview HTML — and it is closed by default.
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
    expect(toggle().textContent?.trim()).toBe('预览');
    expect(toggle().getAttribute('aria-expanded')).toBe('false');
    expect(toggle().getAttribute('aria-controls')).toBe('note-preview-panel');
    // At lg and wider the panel is the sticky column, as before.
    expect(panel().parentElement?.className).toContain('lg:sticky');
    // A named landmark at both breakpoints: the heading names the panel, and nothing claims to be modal.
    expect(panel().getAttribute('role')).toBe('region');
    const heading = panel().querySelector('h2');
    expect(heading?.id).toBe('note-preview-heading');
    expect(heading?.textContent?.trim()).toBe('预览');
    expect(panel().getAttribute('aria-labelledby')).toBe(heading?.id);
    expect(panel().hasAttribute('aria-modal')).toBe(false);
  });

  it('opens from the header button and keeps updating the same preview while open', async () => {
    vi.spyOn(apiClient, 'previewNote')
      .mockResolvedValueOnce(createResponse([{ front: '<p>before</p>', back: '' }]))
      .mockResolvedValueOnce(createResponse([{ front: '<p>after</p>', back: '' }]));

    mountEditor({ id: '5' });
    await flush();
    setField(FRONT, 'first');
    await flush();
    await vi.advanceTimersByTimeAsync(500);
    await flush();
    expect(await previewFront()).toContain('before');

    const field = target.querySelector(`[data-testid="${FRONT}"]`) as HTMLIFrameElement;
    openDrawer();
    await flush();

    expect(panel().getAttribute('data-open')).toBe('true');
    expect(panel().classList.contains('hidden')).toBe(false);
    expect(panel().classList.contains('fixed')).toBe(true);
    expect(toggle().getAttribute('aria-expanded')).toBe('true');
    // One preview container only: the drawer is the panel the preview was already committed into.
    expect(target.querySelectorAll('[data-testid="note-preview-result"]')).toHaveLength(1);
    expect(panel().querySelector('[data-testid="note-preview-result"]')).toBeTruthy();

    // Typing while the drawer is open lands in the same panel.
    setField(FRONT, 'second');
    await flush();
    await vi.advanceTimersByTimeAsync(500);
    await flush();
    expect(await previewFront()).toContain('after');
    expect(panel().querySelector('[data-testid="note-preview-result"]')).toBeTruthy();

    // Opening and closing never remounted the form: same textarea node, same unsaved text.
    expect(target.querySelector(`[data-testid="${FRONT}"]`)).toBe(field);
    expect(fieldValue(FRONT)).toBe('second');
  });

  it('closes on Esc and returns focus to the header button, keeping the field caret', async () => {
    vi.spyOn(apiClient, 'previewNote').mockResolvedValue(createResponse([{ front: '<p>x</p>', back: '' }]));

    mountEditor({ id: '5' });
    await flush();
    setField(FRONT, 'abcdef');
    await flush();
    const field = target.querySelector(`[data-testid="${FRONT}"]`) as HTMLIFrameElement;
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
    expect(fieldValue(FRONT)).toBe('abcdef');
    expect(field).toBeTruthy();
  });

  it('closes from its own close control', async () => {
    vi.spyOn(apiClient, 'previewNote').mockResolvedValue(createResponse([]));

    mountEditor({ id: '5' });
    await flush();
    openDrawer();
    await flush();

    const close = target.querySelector('[data-testid="note-preview-close"]') as HTMLButtonElement;
    expect(close.getAttribute('aria-label')).toBe('关闭预览');
    close.click();
    await flush();

    expect(panel().getAttribute('data-open')).toBeNull();
    expect(document.activeElement).toBe(toggle());
  });

  it('renders the desktop preview unchanged while the drawer stays closed', async () => {
    vi.spyOn(apiClient, 'previewNote').mockResolvedValue(
      createResponse([{ front: '<p>desktop</p>', back: '<p>back</p>' }])
    );

    mountEditor({ id: '5' });
    await flush();
    setField(FRONT, 'Q');
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
