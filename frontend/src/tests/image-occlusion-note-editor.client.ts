import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest';
import { mount, unmount, flushSync } from 'svelte';
import NoteEditView from '../lib/views/NoteEditView.svelte';
import { routeStore } from '../lib/router';
import {
  apiClient,
  type CreateNotesResponse,
  type Note,
  type NotePreviewResponse,
  type NoteType,
  type NoteTypeField,
} from '../lib/api';
import { setLocale } from '../lib/i18n';
import { face } from './card-face-fixture';

/**
 * The note editor with an image-occlusion note type.
 *
 * The acceptance rule is the round trip: shapes created in the editor are saved, the saved field is read
 * back into the editor, and saving again without a change writes the same bytes. The editor's canvas is
 * absent in this environment, so the shape controls drive the same model path a user without a working
 * canvas takes.
 */

vi.mock('../lib/mathjax', () => ({ typeset: vi.fn(async () => {}) }));
vi.mock('../lib/components/ui/toast', () => ({ toast: { success: vi.fn(), warning: vi.fn(), error: vi.fn() } }));

function noteTypeField(id: string, name: string): NoteTypeField {
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
  };
}

/** Anki's image-occlusion type as the server stores it: a cloze type whose template owns the canvas. */
function occlusionNoteType(): NoteType {
  return {
    id: 'nt-occlusion',
    deck_id: '5',
    name: 'Image Occlusion',
    version: 1,
    config: {
      kind: 'cloze',
      fields: [noteTypeField('f0', 'Occlusion'), noteTypeField('f1', 'Image'), noteTypeField('f2', 'Header')],
      templates: [
        {
          id: 't0',
          name: 'Image Occlusion',
          qfmt: '{{cloze:Occlusion}}<div id="image-occlusion-container">{{Image}}<canvas id="image-occlusion-canvas"></canvas></div>',
          afmt: '{{cloze:Occlusion}}<div id="image-occlusion-container">{{Image}}<canvas id="image-occlusion-canvas"></canvas></div>',
        },
      ],
      css: '',
    },
    created_at: '2026-10-01T00:00:00Z',
    updated_at: '2026-10-01T00:00:00Z',
  };
}

function noteFixture(overrides: Partial<Note> = {}): Note {
  return {
    id: '42',
    deck_id: '5',
    kind: '',
    note_type: 'nt-occlusion',
    fields: {},
    tags: [],
    created_at: '2026-10-01T00:00:00Z',
    updated_at: '2026-10-01T00:00:00Z',
    external_ref: '',
    suspended: false,
    ...overrides,
  };
}

function previewResponse(): NotePreviewResponse {
  return { cards: [{ front: face('<div>front</div>'), back: face('<div>back</div>', { side: 'back' }) }] };
}

let target: HTMLElement;
let instance: ReturnType<typeof mount> | null = null;
const fieldValues = new Map<string, string>();
const frames = new WeakMap<HTMLIFrameElement, { postMessage: ReturnType<typeof vi.fn> }>();

async function flush(times = 30): Promise<void> {
  for (let i = 0; i < times; i += 1) await Promise.resolve();
  flushSync();
}

function mountEditor(): void {
  target = document.createElement('div');
  document.body.appendChild(target);
  instance = mount(NoteEditView, { target });
}

function frameFor(testid: string) {
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

/** Writes a rich-text field through its frame, the way typing inside the sandbox does. */
function setField(testid: string, value: string): void {
  const frame = frameFor(testid);
  window.dispatchEvent(new MessageEvent('message', { data: { type: 'ready', source: frame }, source: frame as unknown as MessageEventSource }));
  fieldValues.set(testid, value);
  window.dispatchEvent(new MessageEvent('message', { data: { type: 'change', html: value }, source: frame as unknown as MessageEventSource }));
}

function click(testid: string): void {
  const element = target.querySelector(`[data-testid="${testid}"]`) as HTMLElement | null;
  if (!element) throw new Error(`missing ${testid}`);
  element.click();
}

function submitForm(): void {
  const form = target.querySelector('form') as HTMLFormElement;
  form.dispatchEvent(new Event('submit', { bubbles: true, cancelable: true }));
}

beforeEach(() => {
  setLocale('en');
  vi.clearAllMocks();
  fieldValues.clear();
  vi.spyOn(apiClient, 'listNoteTypes').mockResolvedValue({ note_types: [occlusionNoteType()] });
  vi.spyOn(apiClient, 'previewNote').mockResolvedValue(previewResponse());
  routeStore.set({ path: '/decks/5/notes', params: { id: '5' }, query: {}, search: '', route: null });
});

afterEach(() => {
  if (instance) unmount(instance);
  instance = null;
  vi.restoreAllMocks();
  document.body.innerHTML = '';
});

describe('note editor with an image-occlusion type', () => {
  it('creates two groups of shapes and saves them in Anki form', async () => {
    const createNotes = vi
      .spyOn(apiClient, 'createNotes')
      .mockResolvedValue({ created: 1, updated: 0, skipped: 0, errors: [], dry_run: false } as CreateNotesResponse);

    mountEditor();
    await flush();

    // The mask editor replaces the occlusion field's rich-text control; the image field stays editable.
    expect(target.querySelector('[data-testid="occlusion-editor"]')).toBeTruthy();
    expect(target.querySelector('[data-testid="note-field-Occlusion"]')).toBeNull();

    setField('note-field-Image', '<img src="/fm/token/image.png">');
    await flush();
    expect((target.querySelector('[data-testid="occlusion-stage"] img') as HTMLImageElement | null)?.getAttribute('src')).toBe(
      '/fm/token/image.png',
    );

    click('occlusion-add-rect');
    click('occlusion-add-ellipse');
    await flush();
    // Adding leaves the new shape selected, so release the ellipse before choosing the rectangle.
    click('occlusion-shape-1');
    click('occlusion-shape-0');
    const cloze = target.querySelector('[data-testid="occlusion-cloze"]') as HTMLInputElement;
    cloze.value = '2';
    cloze.dispatchEvent(new Event('input', { bubbles: true }));
    click('occlusion-apply-cloze');
    await flush();

    submitForm();
    await flush();

    expect(createNotes).toHaveBeenCalledTimes(1);
    const request = createNotes.mock.calls[0]![1];
    const fields = request.notes[0]!.fields as Record<string, string>;
    expect(fields.Occlusion).toBe(
      '{{c2::image-occlusion:rect:left=0.1:top=0.1:width=0.2:height=0.12}}' +
        '{{c1::image-occlusion:ellipse:left=0.58:top=0.13:rx=0.1:ry=0.08}}',
    );
    expect(fields.Image).toBe('<img src="/fm/token/image.png">');
  });

  it('reopens the saved note and saves it again without changing the stored field', async () => {
    const stored =
      '{{c2::image-occlusion:rect:left=0.1:top=0.1:width=0.2:height=0.12}}' +
      '{{c1::image-occlusion:ellipse:left=0.58:top=0.13:rx=0.1:ry=0.08}}';
    vi.spyOn(apiClient, 'getNote').mockResolvedValue(
      noteFixture({ fields: { f0: stored, f1: '<img src="/fm/token/image.png">' } }),
    );
    const updateNote = vi.spyOn(apiClient, 'updateNote').mockResolvedValue(noteFixture({ fields: { f0: stored, f1: '<img src="/fm/token/image.png">' } }));

    routeStore.set({ path: '/decks/5/notes', params: { id: '5', noteId: '42' }, query: {}, search: '', route: null });
    mountEditor();
    await flush();

    expect(target.querySelector('[data-testid="occlusion-shape-count"]')?.textContent).toContain('2');
    expect(target.querySelector('[data-testid="occlusion-shape-0"]')?.textContent).toContain('c2');

    submitForm();
    await flush();

    expect(updateNote).toHaveBeenCalledTimes(1);
    const request = updateNote.mock.calls[0]![1];
    expect((request.fields as Record<string, string>).Occlusion).toBe(stored);
  });
});
