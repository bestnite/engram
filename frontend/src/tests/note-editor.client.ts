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
import { toast } from '../lib/components/ui/toast';

// MathJax is taken over by a module-boundary stand-in: the cases assert "typesetting really ran, and only on the preview container",
// without depending on real MathJax assets (the test environment has no self-hosted script).
vi.mock('../lib/mathjax', () => ({ typeset: vi.fn(async () => {}) }));
// Save results go through a toast: the stand-in records calls, and cases assert what was reported.
vi.mock('../lib/components/ui/toast', () => ({ toast: { success: vi.fn(), warning: vi.fn(), error: vi.fn() } }));

const typesetSpy = vi.mocked(typeset);
const SHA = 'a'.repeat(64);
const PREVIEW_URL = `/media/${SHA}`;

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

function createResponse(cards: Array<{ front_html: string; back_html: string }>): NotePreviewResponse {
  return { cards };
}

let target: HTMLElement;
const mountedInstances: Array<ReturnType<typeof mount>> = [];

function mountEditor(params: Record<string, string>): void {
  seedCardTypes();
  routeStore.set({ path: '/decks/5/notes', params, query: {}, search: '', route: null });
  target = document.createElement('div');
  document.body.appendChild(target);
  mountedInstances.push(mount(NoteEditView, { target }));
}

/** Flush microtasks and apply Svelte's pending state synchronously so assertions see the real DOM. */
async function flush(times = 24): Promise<void> {
  for (let i = 0; i < times; i++) await Promise.resolve();
  flushSync();
}

function setField(testid: string, value: string): void {
  const el = target.querySelector(`[data-testid="${testid}"]`) as HTMLInputElement | HTMLTextAreaElement;
  el.value = value;
  el.dispatchEvent(new Event('input', { bubbles: true }));
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

function previewFront(index = 0): string {
  return target.querySelector(`[data-testid="note-preview-front-${index}"]`)?.textContent ?? '';
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
    expect(previewNote).toHaveBeenCalledWith('5', 'basic', { front: 'Q' });
    expect(previewFront()).toContain('rendered Q');
    expect(target.querySelector('[data-testid="note-preview-result"]')).toBeTruthy();
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
    expect(previewNote).toHaveBeenCalledWith('5', 'basic', { front: 'QR' });
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
    expect(previewFront()).toContain('SECOND');

    // The old request only comes back now: it must be dropped and must not overwrite the newer result.
    first.resolve(createResponse([{ front_html: '<p>FIRST</p>', back_html: '' }]));
    await flush();
    expect(previewFront()).toContain('SECOND');
    expect(previewFront()).not.toContain('FIRST');
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
    expect(previewFront()).toContain('GOOD');

    // The stale request then fails: it must not turn a ready preview into an error state.
    first.reject(new ApiClientError('HTTP 500', { status: 500, code: 'internal_error' }));
    await flush();
    expect(previewFront()).toContain('GOOD');
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
    setField('note-field-value', '0');
    await flush();

    await vi.advanceTimersByTimeAsync(500);
    await flush();

    expect(previewNote).toHaveBeenCalledWith('5', 'numeric', { value: 0 });
  });

  it('typesets only the sanitized preview container through MathJax', async () => {
    vi.spyOn(apiClient, 'previewNote').mockResolvedValue(
      createResponse([{ front_html: '<p>$x$</p>', back_html: '' }])
    );

    mountEditor({ id: '5' });
    await flush();
    setField('note-field-front', 'Q');
    await flush();
    await vi.advanceTimersByTimeAsync(500);
    await flush();

    expect(typesetSpy).toHaveBeenCalled();
    const container = target.querySelector('[data-testid="note-preview-result"]');
    expect(container).toBeTruthy();
    const [nodes] = typesetSpy.mock.calls.at(-1)!;
    expect(nodes).toEqual([container]);
  });

  it('enhances the same preview container it typesets with shortcode controls', async () => {
    const sha = 'a'.repeat(64);
    vi.spyOn(apiClient, 'previewNote').mockResolvedValue(
      createResponse([
        {
          front_html: `<p><span class="engram-audio" data-audio-src="/media/${sha}" data-audio-start="12.5">/media/${sha}</span></p>`,
          back_html: '',
        },
      ])
    );

    mountEditor({ id: '5' });
    await flush();
    setField('note-field-front', 'Q');
    await flush();
    await vi.advanceTimersByTimeAsync(500);
    await flush();

    const container = target.querySelector('[data-testid="note-preview-result"]');
    expect(container).toBeTruthy();
    // The preview is the one sanitized container in the editor: it is the one that was typeset, and the control
    // appears inside it rather than anywhere the raw field text lives.
    const [nodes] = typesetSpy.mock.calls.at(-1)!;
    expect(nodes).toEqual([container]);
    expect(container!.querySelector('button[data-shortcode-control="audio"]')).toBeTruthy();
    expect(container!.textContent).toContain(`/media/${sha}`);
  });

  it('serializes commit and typeset so a delayed typeset only ever sees its own generation', async () => {
    // The first typesetting round hangs: if "write the new card" and typesetting are not serialized, the second response would replace the container
    // midway through round one and round one's MathJax would rewrite the DOM round two just committed. Each typesetting call records the container's actual text.
    const firstTypeset = deferred<void>();
    const seen: string[] = [];
    typesetSpy.mockImplementation(async (nodes) => {
      seen.push((nodes[0] as HTMLElement | null)?.textContent ?? '');
      if (seen.length === 1) await firstTypeset.promise;
    });
    vi.spyOn(apiClient, 'previewNote')
      .mockResolvedValueOnce(createResponse([{ front_html: '<p>FIRST</p>', back_html: '' }]))
      .mockResolvedValueOnce(createResponse([{ front_html: '<p>SECOND</p>', back_html: '' }]));

    mountEditor({ id: '5' });
    await flush();

    setField('note-field-front', 'A');
    await flush();
    await vi.advanceTimersByTimeAsync(500);
    await flush();
    expect(previewFront()).toContain('FIRST');
    expect(seen).toHaveLength(1);
    expect(seen[0]).toContain('FIRST');

    setField('note-field-front', 'B');
    await flush();
    await vi.advanceTimersByTimeAsync(500);
    await flush();

    // The second response has arrived but the typesetting chain is still held by round one: what is displayed must still be round one's content, and
    // round two must not have started typesetting yet (otherwise round one's delayed pass would land on round two's container).
    expect(previewFront()).toContain('FIRST');
    expect(seen).toHaveLength(1);

    firstTypeset.resolve();
    await flush();

    // Round two commits and typesets only after round one's typesetting ends: the newest content is shown, and typesetting sees its own generation.
    expect(previewFront()).toContain('SECOND');
    expect(seen).toHaveLength(2);
    expect(seen[1]).toContain('SECOND');
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
    setField('note-tags-editor', 'topic, extra');
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
    expect((target.querySelector('[data-testid="note-field-front"]') as HTMLTextAreaElement).value).toBe('kept');
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
    // The tools sit in the label row of the field they write into, and no dialog is open until one is asked for.
    expect(target.querySelector('[data-testid="note-field-front-insert-image"]')).toBeTruthy();
    expect(target.querySelector('[data-testid="note-field-front-insert-shortcode"]')).toBeTruthy();
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

    expect((target.querySelector('[data-testid="note-field-front"]') as HTMLTextAreaElement).value).toBe('Q');

    await vi.advanceTimersByTimeAsync(500);
    await flush();

    expect(previewNote).toHaveBeenCalledWith('5', 'basic', { front: 'Q', back: 'A' });
    expect(previewFront()).toContain('Q');
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
    setField('note-tags-editor', 'a, b');
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
    expect((target.querySelector('[data-testid="note-field-front"]') as HTMLTextAreaElement).value).toBe('Q2');
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

    expect((target.querySelector('[data-testid="note-field-front"]') as HTMLTextAreaElement).value).toBe('QQ-newer');
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
    pending.resolve(createResponse([{ front_html: '<p>late</p>', back_html: '' }]));
    await flush();

    // Results landing after unmount must not trigger MathJax typesetting (nor write state).
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

    const typesetCallsBefore = typesetSpy.mock.calls.length;
    unmount(mountedInstances.pop()!);

    // The save request lands only after unmount: it must write no state and trigger no typesetting.
    pending.resolve(noteFixture({ fields: { front: 'Q2', back: 'A' } }));
    await flush();

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
 * Insertion into a card field. Both tools are opened from the label row of the field they write into,
 * and the caret that field reported decides where the text lands. The image dialog writes the Markdown
 * image form, the composer writes source text, and neither introduces a second field selector.
 */
describe('unified note editor — inserting content', () => {
  function fieldValue(testid: string): string {
    return (target.querySelector(`[data-testid="${testid}"]`) as HTMLTextAreaElement).value;
  }

  /** Places a real caret and reports it, the way a click inside the field does. */
  function setCaret(testid: string, start: number, end: number): void {
    const field = target.querySelector(`[data-testid="${testid}"]`) as HTMLTextAreaElement;
    field.focus();
    field.setSelectionRange(start, end);
    field.dispatchEvent(new Event('select', { bubbles: true }));
    flushSync();
  }

  function toolbarButton(key: string, action: 'image' | 'shortcode'): HTMLButtonElement {
    return target.querySelector(`[data-testid="note-field-${key}-insert-${action}"]`) as HTMLButtonElement;
  }

  /** The dialog is portalled to body, so its own queries never start at the mounted view. */
  function dialog(): HTMLElement | null {
    return document.body.querySelector('[data-testid="note-insert-dialog"]');
  }

  function inDialog<T extends Element = HTMLElement>(testid: string): T {
    const node = document.body.querySelector(`[data-testid="${testid}"]`);
    if (!node) throw new Error(`missing [data-testid="${testid}"] inside the insertion dialog`);
    return node as T;
  }

  /** Opens the dialog from a field's own toolbar and hands back the button that must regain focus. */
  function openDialog(key: string, action: 'image' | 'shortcode'): HTMLButtonElement {
    const button = toolbarButton(key, action);
    button.focus();
    button.click();
    flushSync();
    return button;
  }

  function click(node: Element): void {
    node.dispatchEvent(new PointerEvent('pointerdown', { bubbles: true, cancelable: true }));
    node.dispatchEvent(new PointerEvent('pointerup', { bubbles: true, cancelable: true }));
    node.dispatchEvent(new MouseEvent('click', { bubbles: true, cancelable: true }));
    flushSync();
  }

  function typeTtsBody(body: string): void {
    const input = inDialog<HTMLInputElement>('shortcode-tts-body');
    input.value = body;
    input.dispatchEvent(new Event('input', { bubbles: true }));
    flushSync();
  }

  function insertShortcode(): void {
    click(inDialog('shortcode-insert-submit'));
  }

  function dropFile(testid: string, file: File): void {
    const input = inDialog<HTMLInputElement>(testid);
    Object.defineProperty(input, 'files', { configurable: true, value: [file] });
    input.dispatchEvent(new Event('change', { bubbles: true }));
  }

  /** A closed dialog leaves the DOM one animation frame later, and the fake clock owns that frame. */
  async function settleDialog(): Promise<void> {
    await vi.advanceTimersByTimeAsync(50);
    await flush();
  }

  it('inserts a picked library picture into the field whose toolbar opened the dialog', async () => {
    vi.spyOn(apiClient, 'listMedia').mockResolvedValue({
      items: [
        { sha256: 'b'.repeat(64), mime: 'audio/mpeg', bytes: 9, url: `/media/${'b'.repeat(64)}`, created_at: '' },
        { sha256: SHA, mime: 'image/png', bytes: 4, url: PREVIEW_URL, created_at: '' },
      ],
      next_cursor: '',
    });
    const previewNote = vi.spyOn(apiClient, 'previewNote').mockResolvedValue(createResponse([]));

    mountEditor({ id: '5' });
    await flush();
    setCaret('note-field-front', 0, 0);

    const trigger = openDialog('front', 'image');
    expect(dialog()).toBeTruthy();
    click(inDialog('note-insert-image-picker-toggle'));
    await flush();

    // The dialog writes the Markdown image form, so pictures are the only thing it offers.
    expect(document.body.querySelector(`[data-testid="note-insert-image-item-${SHA}"]`)).toBeTruthy();
    expect(document.body.querySelector(`[data-testid="note-insert-image-item-${'b'.repeat(64)}"]`)).toBeNull();

    click(inDialog(`note-insert-image-item-${SHA}`));
    await flush();

    expect(fieldValue('note-field-front')).toBe(`![](${PREVIEW_URL})`);
    await settleDialog();
    expect(dialog()).toBeNull();
    // The dialog closes on the insert and hands focus back to the button that opened it.
    expect(document.activeElement).toBe(trigger);

    await vi.advanceTimersByTimeAsync(500);
    await flush();
    expect(previewNote).toHaveBeenLastCalledWith('5', 'basic', { front: `![](${PREVIEW_URL})` });
  });

  it('uploads a picture into the field and refuses a non-image in the image dialog', async () => {
    const upload = vi.spyOn(apiClient, 'uploadDeckMedia').mockResolvedValue({
      sha256: SHA,
      mime: 'image/png',
      bytes: 4,
      url: PREVIEW_URL,
    });
    vi.spyOn(apiClient, 'previewNote').mockResolvedValue(createResponse([]));

    mountEditor({ id: '5' });
    await flush();

    const trigger = openDialog('front', 'image');
    dropFile('note-insert-image-input', new File(['sound'], 'sound.mp3', { type: 'audio/mpeg' }));
    await flush();

    // Refused before any request, with a reason, and the dialog stays open on this field.
    expect(upload).not.toHaveBeenCalled();
    expect(toast.error).toHaveBeenCalledWith(formatMessage('zh-CN', 'media.upload.image_only'));
    expect(dialog()).toBeTruthy();
    expect(fieldValue('note-field-front')).toBe('');

    dropFile('note-insert-image-input', new File(['picture'], 'picture.png', { type: 'image/png' }));
    await flush();

    expect(upload).toHaveBeenCalledWith('5', expect.any(File));
    expect(fieldValue('note-field-front')).toBe(`![](${PREVIEW_URL})`);
    await settleDialog();
    expect(dialog()).toBeNull();
    expect(document.activeElement).toBe(trigger);
    expect(toast.success).toHaveBeenCalledWith(formatMessage('zh-CN', 'media.upload.inserted'));
  });

  it('writes no image reference when the server detects a different type in the bytes', async () => {
    // The upload endpoint accepts audio too: a file named .png can come back as a sound, and the image
    // form would then point at bytes the browser cannot draw.
    vi.spyOn(apiClient, 'uploadDeckMedia').mockResolvedValue({
      sha256: 'b'.repeat(64),
      mime: 'audio/mpeg',
      bytes: 9,
      url: `/media/${'b'.repeat(64)}`,
    });
    vi.spyOn(apiClient, 'previewNote').mockResolvedValue(createResponse([]));

    mountEditor({ id: '5' });
    await flush();

    openDialog('front', 'image');
    dropFile('note-insert-image-input', new File(['sound'], 'sound.png', { type: 'image/png' }));
    await flush();

    expect(toast.error).toHaveBeenCalledWith(formatMessage('zh-CN', 'media.upload.image_only'));
    expect(fieldValue('note-field-front')).toBe('');
    expect(dialog()).toBeTruthy();
  });

  it('splices a shortcode at the caret of the field whose toolbar opened the dialog', async () => {
    vi.spyOn(apiClient, 'previewNote').mockResolvedValue(createResponse([]));

    mountEditor({ id: '5' });
    await flush();
    setField('note-field-front', 'abcdef');
    await flush();
    setCaret('note-field-front', 2, 2);

    const trigger = openDialog('front', 'shortcode');
    // The dialog names the one field it writes into, and there is no field selector anywhere in it.
    expect(dialog()!.textContent).toContain(
      formatMessage('zh-CN', 'shortcode.insert.target', { field: formatMessage('zh-CN', 'note.fields.front') })
    );
    expect(dialog()!.querySelector('[data-testid="media-field"]')).toBeNull();
    // The composer is titled by the dialog, so it does not print its own heading a second time.
    expect(dialog()!.querySelector('h2')).toBeNull();

    typeTtsBody('x');
    expect(inDialog('shortcode-insert-preview').textContent).toBe('{{tts:x}}');
    insertShortcode();
    await flush();

    expect(fieldValue('note-field-front')).toBe('ab{{tts:x}}cdef');
    await settleDialog();
    expect(dialog()).toBeNull();
    expect(document.activeElement).toBe(trigger);
  });

  it('replaces the selected text of the target field', async () => {
    vi.spyOn(apiClient, 'previewNote').mockResolvedValue(createResponse([]));

    mountEditor({ id: '5' });
    await flush();
    setField('note-field-front', 'abcdef');
    await flush();
    setCaret('note-field-front', 1, 4);

    openDialog('front', 'shortcode');
    typeTtsBody('x');
    insertShortcode();

    expect(fieldValue('note-field-front')).toBe('a{{tts:x}}ef');
  });

  it('appends on a new line when the target field has no known caret', async () => {
    vi.spyOn(apiClient, 'getNote').mockResolvedValue(noteFixture());
    vi.spyOn(apiClient, 'previewNote').mockResolvedValue(createResponse([]));

    mountEditor({ id: '5', noteId: '9' });
    await flush();

    openDialog('front', 'shortcode');
    typeTtsBody('x');
    insertShortcode();
    await flush();

    expect(fieldValue('note-field-front')).toBe('Q\n{{tts:x}}');
    // One assignment per insertion is enough for the live preview to reschedule.
    await vi.advanceTimersByTimeAsync(500);
    await flush();
    expect(apiClient.previewNote).toHaveBeenLastCalledWith('5', 'basic', { front: 'Q\n{{tts:x}}', back: 'A' });
  });

  it('writes into the field whose own toolbar was used, not the one holding the caret', async () => {
    vi.spyOn(apiClient, 'previewNote').mockResolvedValue(createResponse([]));

    mountEditor({ id: '5' });
    await flush();
    setField('note-field-front', 'front-text');
    await flush();
    setCaret('note-field-front', 5, 5);

    openDialog('back', 'shortcode');
    typeTtsBody('x');
    insertShortcode();
    await flush();

    expect(fieldValue('note-field-back')).toBe('{{tts:x}}');
    expect(fieldValue('note-field-front')).toBe('front-text');
  });

  it('keeps the insert button unavailable while the dialog input is invalid', async () => {
    vi.spyOn(apiClient, 'previewNote').mockResolvedValue(createResponse([]));

    mountEditor({ id: '5' });
    await flush();

    openDialog('front', 'shortcode');
    const button = inDialog<HTMLButtonElement>('shortcode-insert-submit');
    expect(button.disabled).toBe(true);
    typeTtsBody('x');
    expect(button.disabled).toBe(false);
  });

  it('offers no toolbar on a lines field, which stores an array', async () => {
    mountEditor({ id: '5' });
    await flush();

    await selectKind(formatMessage('zh-CN', 'notes.kind.list'));

    expect(target.querySelector('[data-testid="note-field-prompt-toolbar"]')).toBeTruthy();
    expect(target.querySelector('[data-testid="note-field-items-toolbar"]')).toBeNull();
    expect(target.querySelector('[data-testid="note-field-ordered-toolbar"]')).toBeNull();
  });

});
