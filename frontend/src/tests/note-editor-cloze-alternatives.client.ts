import { describe, it, expect, beforeEach, afterEach, vi } from 'vitest';
import { mount, unmount, flushSync } from 'svelte';
import NoteEditView from '../lib/views/NoteEditView.svelte';
import { apiClient, type NoteType, type Note } from '../lib/api';
import { setLocale } from '../lib/i18n';
import { routeStore, matchRoute } from '../lib/router';

describe('NoteEditView cloze alternatives integration', () => {
  let instance: ReturnType<typeof mount> | null = null;
  let target: HTMLElement;
  let restoreIframeStub: (() => void) | null = null;

  beforeEach(() => {
    const origSet = HTMLIFrameElement.prototype.setAttribute;
    const desc = Object.getOwnPropertyDescriptor(HTMLIFrameElement.prototype, 'src');

    HTMLIFrameElement.prototype.setAttribute = function (name: string, val: string) {
      if (name === 'src' && (val.includes('/card-frame') || val.includes('/edit-frame'))) {
        this.srcdoc = '<!doctype html><html><body></body></html>';
        return;
      }
      return origSet.call(this, name, val);
    };

    if (desc?.set) {
      Object.defineProperty(HTMLIFrameElement.prototype, 'src', {
        get: desc.get,
        set(val: string) {
          if (typeof val === 'string' && (val.includes('/card-frame') || val.includes('/edit-frame'))) {
            this.srcdoc = '<!doctype html><html><body></body></html>';
            return;
          }
          desc.set?.call(this, val);
        },
        configurable: true,
      });
    }

    restoreIframeStub = () => {
      HTMLIFrameElement.prototype.setAttribute = origSet;
      if (desc) Object.defineProperty(HTMLIFrameElement.prototype, 'src', desc);
    };
  });

  afterEach(() => {
    if (restoreIframeStub) restoreIframeStub();
    if (instance) unmount(instance);
    instance = null;
    document.body.innerHTML = '';
    vi.restoreAllMocks();
  });

  async function flush(): Promise<void> {
    for (let i = 0; i < 20; i++) await Promise.resolve();
    flushSync();
  }

  const clozeTypeWithMeta: NoteType = {
    id: 'nt_cloze',
    deck_id: 'd1',
    name: 'Cloze With Meta',
    version: 1,
    config: {
      kind: 'cloze',
      fields: [
        {
          id: 'f_text',
          name: 'Text',
          sticky: false,
          rtl: false,
          font: '',
          font_size: 0,
          description: '',
          plain_text: false,
          collapsed: false,
          exclude_from_search: false,
          tag: null,
          prevent_deletion: false,
        },
        {
          id: 'f_meta',
          name: 'engram_meta',
          sticky: false,
          rtl: false,
          font: '',
          font_size: 0,
          description: '',
          plain_text: true,
          collapsed: true,
          exclude_from_search: true,
          tag: null,
          prevent_deletion: false,
        },
      ],
      templates: [
        {
          id: 't_cloze',
          name: 'Cloze',
          qfmt: '{{cloze:Text}}',
          afmt: '{{cloze:Text}}',
          answer: {
            control: 'cloze_blanks',
            bindings: { text: 'f_text' },
          },
        },
      ],
      css: '',
    },
    created_at: '2026-01-01T00:00:00Z',
    updated_at: '2026-01-01T00:00:00Z',
  };

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

  function fromFrame(frame: FakeFrame, message: Record<string, unknown>): void {
    window.dispatchEvent(new MessageEvent('message', { data: message, source: frame as unknown as MessageEventSource }));
  }

  function setField(testid: string, value: string): void {
    const frame = frameFor(testid);
    fromFrame(frame, { type: 'ready' });
    fromFrame(frame, { type: 'change', html: value });
  }

  it('renders structured editor instead of rich-text for engram_meta, hard asserts blanks, and submits raw JSON on save', async () => {
    setLocale('zh-CN');
    routeStore.set(matchRoute('/decks/d1/notes/new'));
    vi.spyOn(apiClient, 'listNoteTypes').mockResolvedValue({ note_types: [clozeTypeWithMeta] } as any);
    vi.spyOn(apiClient, 'frameMediaBase').mockResolvedValue('');
    vi.spyOn(apiClient, 'previewNoteDraft').mockResolvedValue({ cards: [] });
    const createSpy = vi.spyOn(apiClient, 'createNotes').mockResolvedValue({
      created: 1,
      dry_run: false,
      errors: [],
      notes: [],
    } as any);

    target = document.createElement('div');
    document.body.appendChild(target);
    instance = mount(NoteEditView, { target });
    await flush();

    // 1. Verify engram_meta is NOT rendered as a rich-text / field input in NoteFieldsForm
    const metaRichField = target.querySelector('[data-testid="note-field-engram_meta"]');
    expect(metaRichField).toBeNull();

    // 2. ClozeAlternativesEditor is mounted
    const clozeAltEditor = target.querySelector('[data-testid="cloze-alternatives-editor"]');
    expect(clozeAltEditor).not.toBeNull();

    // 3. Edit field Text via real frame message
    setField('note-field-Text', '<p>{{c1::Paris}} is capital of {{c2::France}}.</p>');
    await flush();

    // 4. Hard assert blanks without any conditional skip
    const canonicals = Array.from(target.querySelectorAll('[data-testid="cloze-alt-canonical"]')).map(
      (el) => el.textContent?.trim()
    );
    expect(canonicals).toEqual(['Paris', 'France']);

    // 5. Add alternative to Paris
    const input = target.querySelector('input[data-testid="cloze-alt-input-1-0"]') as HTMLInputElement;
    expect(input).not.toBeNull();
    input.value = 'City of Light';
    input.dispatchEvent(new Event('input', { bubbles: true }));
    const addBtn = target.querySelector('button[data-testid="cloze-alt-add-btn-1-0"]') as HTMLButtonElement;
    expect(addBtn).not.toBeNull();
    addBtn.click();
    await flush();

    const pills = target.querySelectorAll('[data-testid="cloze-alt-pill"]');
    expect(pills).toHaveLength(1);
    expect(pills[0]!.textContent).toContain('City of Light');

    // 6. Click Save (submit)
    const submitBtn = target.querySelector('[data-testid="note-submit"]') as HTMLButtonElement;
    expect(submitBtn).not.toBeNull();
    submitBtn.click();
    await flush();

    // 7. Verify API call payload
    expect(createSpy).toHaveBeenCalledTimes(1);
    const submittedNotes = createSpy.mock.calls[0]![1].notes;
    expect(submittedNotes).toHaveLength(1);
    const submittedFields = submittedNotes[0]!.fields;
    expect(submittedFields.Text).toBe('<p>{{c1::Paris}} is capital of {{c2::France}}.</p>');

    const meta = JSON.parse(submittedFields.engram_meta as string);
    expect(meta.cloze_accept['1']['Paris']).toEqual(['City of Light']);
    expect(meta.cloze_accept['2']?.['France'] ?? []).toEqual([]);
  });

  it('loads existing note by NAME contract, preserves unknown keys and invalid subtrees, and saves edited siblings', async () => {
    setLocale('zh-CN');
    routeStore.set(matchRoute('/decks/d1/notes/n_edit'));

    const rawMeta = JSON.stringify({
      custom_extension: { flags: [1, 2, 3] },
      ['__proto__']: { nested: 'danger' },
      cloze_accept: {
        '1': {
          Paris: ['City of Light', 999], // mixed array (invalid entry)
          Rome: ['Eternal City'],         // valid sibling
        },
        '2': 'invalid_group_string',     // invalid group subtree
      },
    });

    const loadedNote: Note = {
      id: 'n_edit',
      deck_id: 'd1',
      note_type: 'nt_cloze',
      fields: {
        Text: '<p>{{c1::Paris}} and {{c1::Rome}}.</p>',
        engram_meta: rawMeta,
      },
      tags: [],
      created_at: '',
      updated_at: '',
      external_ref: null,
      suspended: false,
    };

    vi.spyOn(apiClient, 'getNote').mockResolvedValue(loadedNote);
    vi.spyOn(apiClient, 'listNoteTypes').mockResolvedValue({ note_types: [clozeTypeWithMeta] } as any);
    vi.spyOn(apiClient, 'frameMediaBase').mockResolvedValue('');
    vi.spyOn(apiClient, 'previewNoteDraft').mockResolvedValue({ cards: [] });
    const updateSpy = vi.spyOn(apiClient, 'updateNote').mockResolvedValue(loadedNote);

    target = document.createElement('div');
    document.body.appendChild(target);
    instance = mount(NoteEditView, { target });
    await flush();

    // Verify existing pills loaded
    const canonicals = Array.from(target.querySelectorAll('[data-testid="cloze-alt-canonical"]')).map(
      (el) => el.textContent?.trim()
    );
    expect(canonicals).toEqual(['Paris', 'Rome']);

    // Mixed array warning shown on Paris
    const warn = target.querySelector('[data-testid="cloze-alt-invalid-entry-warning"]');
    expect(warn).not.toBeNull();

    // Edit valid sibling Rome by adding "Roma"
    const romeInput = target.querySelector('input[data-testid="cloze-alt-input-1-1"]') as HTMLInputElement;
    expect(romeInput).not.toBeNull();
    romeInput.value = 'Roma';
    romeInput.dispatchEvent(new Event('input', { bubbles: true }));
    const romeAddBtn = target.querySelector('button[data-testid="cloze-alt-add-btn-1-1"]') as HTMLButtonElement;
    expect(romeAddBtn).not.toBeNull();
    romeAddBtn.click();
    await flush();

    // Click Save
    const saveBtn = target.querySelector('[data-testid="note-submit"]') as HTMLButtonElement;
    expect(saveBtn).not.toBeNull();
    saveBtn.click();
    await flush();

    // Hard assert updateNote payload
    expect(updateSpy).toHaveBeenCalledTimes(1);
    const submittedMetaStr = updateSpy.mock.calls[0]![1].fields.engram_meta as string;
    const submittedMeta = JSON.parse(submittedMetaStr);

    // 1. Unknown root keys and special prototype keys preserved
    expect(submittedMeta.custom_extension).toEqual({ flags: [1, 2, 3] });
    expect(submittedMeta.__proto__).toEqual({ nested: 'danger' });
    // 2. Edited sibling Rome is updated
    expect(submittedMeta.cloze_accept['1']['Rome']).toEqual(['Eternal City', 'Roma']);
    // 3. Un-repaired mixed array on Paris is preserved untouched
    expect(submittedMeta.cloze_accept['1']['Paris']).toEqual(['City of Light', 999]);
    // 4. Invalid group preserved untouched
    expect(submittedMeta.cloze_accept['2']).toBe('invalid_group_string');

    // Reload exactly the value sent to the server, rather than the original fixture.
    unmount(instance!);
    instance = null;
    document.body.innerHTML = '';
    vi.mocked(apiClient.getNote).mockResolvedValue({ ...loadedNote, fields: { ...loadedNote.fields, engram_meta: submittedMetaStr } });
    target = document.createElement('div');
    document.body.appendChild(target);
    instance = mount(NoteEditView, { target });
    await flush();
    (target.querySelector('[data-testid="note-submit"]') as HTMLButtonElement).click();
    await flush();
    const reloadedMeta = JSON.parse(updateSpy.mock.calls.at(-1)![1].fields.engram_meta as string);
    expect(reloadedMeta.custom_extension).toEqual({ flags: [1, 2, 3] });
    expect(reloadedMeta.cloze_accept['1'].Rome).toEqual(['Eternal City', 'Roma']);
  });

  it('preserves malformed raw JSON on direct save, or emits valid empty cloze_accept after explicit reset', async () => {
    setLocale('zh-CN');
    routeStore.set(matchRoute('/decks/d1/notes/n_malformed'));

    const malformedNote: Note = {
      id: 'n_malformed',
      deck_id: 'd1',
      note_type: 'nt_cloze',
      fields: {
        Text: '<p>{{c1::Paris}}.</p>',
        engram_meta: '{ broken: invalid json',
      },
      tags: [],
      created_at: '',
      updated_at: '',
      external_ref: null,
      suspended: false,
    };

    vi.spyOn(apiClient, 'getNote').mockResolvedValue(malformedNote);
    vi.spyOn(apiClient, 'listNoteTypes').mockResolvedValue({ note_types: [clozeTypeWithMeta] } as any);
    vi.spyOn(apiClient, 'frameMediaBase').mockResolvedValue('');
    vi.spyOn(apiClient, 'previewNoteDraft').mockResolvedValue({ cards: [] });
    const updateSpy = vi.spyOn(apiClient, 'updateNote').mockResolvedValue(malformedNote);

    target = document.createElement('div');
    document.body.appendChild(target);
    instance = mount(NoteEditView, { target });
    await flush();

    // Malformed banner is displayed
    expect(target.querySelector('[data-testid="cloze-alt-malformed-banner"]')).not.toBeNull();

    // Change ordinary content and tags before saving the untouched malformed metadata.
    setField('note-field-Text', '<p>{{c1::Paris}} changed.</p>');
    const tags = target.querySelector('[data-testid="note-tags-editor"]') as HTMLInputElement;
    tags.value = 'changed';
    tags.dispatchEvent(new Event('input', { bubbles: true }));
    await flush();
    const saveBtn = target.querySelector('[data-testid="note-submit"]') as HTMLButtonElement;
    expect(saveBtn).not.toBeNull();
    saveBtn.click();
    await flush();

    expect(updateSpy).toHaveBeenCalledTimes(1);
    expect(updateSpy.mock.calls[0]![1].fields.engram_meta).toBe('{ broken: invalid json');
    expect(updateSpy.mock.calls[0]![1].fields.Text).toBe('<p>{{c1::Paris}} changed.</p>');
    expect(updateSpy.mock.calls[0]![1].tags).toContain('changed');

    // 2. Click reset and confirm in modal
    const resetBtn = target.querySelector('[data-testid="cloze-alt-reset-btn"]') as HTMLButtonElement;
    expect(resetBtn).not.toBeNull();
    resetBtn.click();
    await flush();

    const confirmBtn = document.body.querySelector('[data-testid="cloze-alt-reset-confirm"]') as HTMLButtonElement;
    expect(confirmBtn).not.toBeNull();
    confirmBtn.click();
    await flush();

    // Banner is removed
    expect(target.querySelector('[data-testid="cloze-alt-malformed-banner"]')).toBeNull();

    // 3. Save again: now emits valid clean JSON
    saveBtn.click();
    await flush();

    expect(updateSpy).toHaveBeenCalledTimes(2);
    const resetMetaStr = updateSpy.mock.calls[1]![1].fields.engram_meta as string;
    const resetMeta = JSON.parse(resetMetaStr);
    expect(resetMeta).toEqual({ cloze_accept: {} });
  });
});
