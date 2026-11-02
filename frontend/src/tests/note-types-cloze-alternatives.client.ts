import { describe, it, expect, beforeEach, afterEach, vi } from 'vitest';
import { mount, unmount, flushSync } from 'svelte';
import NoteTypesView from '../lib/views/NoteTypesView.svelte';
import NoteEditView from '../lib/views/NoteEditView.svelte';
import { ApiClientError, type NoteType, type NoteTypeField } from '../lib/api';
import { setLocale } from '../lib/i18n';
import { routeStore, matchRoute } from '../lib/router';
import { apiClient } from '../lib/api';

/**
 * The engram_meta field is built in for a note type whose template uses the cloze_blanks answer control.
 * The note-type editor must present it as a locked row: no enable/repair action, no rename, no move, no
 * delete, no field options, no answer-role binding and no row in the change-note-type field selector.
 */
describe('NoteTypesView built-in cloze metadata field', () => {
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

  function field(overrides: Partial<NoteTypeField> & { id: string; name: string }): NoteTypeField {
    return {
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
      ...overrides,
    };
  }

  function textField(id = 'f_text'): NoteTypeField {
    return field({ id, name: 'Text' });
  }

  function metaField(id = 'f_meta'): NoteTypeField {
    return field({
      id,
      name: 'engram_meta',
      plain_text: true,
      collapsed: true,
      exclude_from_search: true,
      // A value the editor must not touch while the row is locked.
      description: 'managed by the server',
    });
  }

  function clozeNoteType(): NoteType {
    return {
      id: 'nt1',
      deck_id: 'd1',
      name: 'Cloze Type',
      version: 1,
      config: {
        kind: 'cloze',
        fields: [textField(), metaField()],
        templates: [
          {
            id: 't1',
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
  }

  /** A second cloze type the change dialog can target, carrying its own built-in metadata field. */
  function otherClozeType(): NoteType {
    return {
      id: 'nt2',
      deck_id: 'd1',
      name: 'Other Cloze Type',
      version: 1,
      config: {
        kind: 'cloze',
        fields: [textField('tg_text'), metaField('tg_meta')],
        templates: [
          {
            id: 'tg_t1',
            name: 'Cloze',
            qfmt: '{{cloze:Text}}',
            afmt: '{{cloze:Text}}',
            answer: {
              control: 'cloze_blanks',
              bindings: { text: 'tg_text' },
            },
          },
        ],
        css: '',
      },
      created_at: '2026-01-01T00:00:00Z',
      updated_at: '2026-01-01T00:00:00Z',
    };
  }

  async function setup(types: NoteType[] = [clozeNoteType()]) {
    setLocale('zh-CN');
    routeStore.set(matchRoute('/decks/d1/note-types'));
    target = document.createElement('div');
    document.body.appendChild(target);
    instance = mount(NoteTypesView, {
      target,
      props: {
        initialLoading: false,
        initialNoteTypes: types,
        initialStocks: [],
        initialControls: [
          {
            name: 'cloze_blanks',
            label_key: 'notetype.answer.control.cloze_blanks',
            roles: [{ name: 'text', kind: 'text', required: true, label_key: 'notetype.answer.role.text' }],
          },
        ],
      },
    });
    await flush();
    return { target };
  }

  it('offers no enable or repair action for a cloze_blanks note type', async () => {
    await setup();
    expect(target.querySelector('[data-testid="notetype-cloze-alternatives"]')).toBeNull();
    expect(target.querySelector('[data-testid="notetype-cloze-alternatives-enable"]')).toBeNull();
    expect(target.querySelector('[data-testid="notetype-cloze-alternatives-fix-flag"]')).toBeNull();
    expect(target.querySelector('[data-testid="notetype-cloze-alternatives-status"]')).toBeNull();
  });

  it('shows the built-in metadata field as a visible, locked row', async () => {
    await setup();

    const row = target.querySelector('[data-testid="notetype-field-f_meta"]');
    expect(row).not.toBeNull();

    const name = target.querySelector('[data-testid="notetype-field-name-f_meta"]') as HTMLInputElement;
    expect(name).not.toBeNull();
    expect(name.value).toBe('engram_meta');
    expect(name.readOnly).toBe(true);

    expect((target.querySelector('[data-testid="notetype-field-up-f_meta"]') as HTMLButtonElement).disabled).toBe(true);
    expect((target.querySelector('[data-testid="notetype-field-down-f_meta"]') as HTMLButtonElement).disabled).toBe(true);
    expect((target.querySelector('[data-testid="notetype-field-delete-f_meta"]') as HTMLButtonElement).disabled).toBe(true);

    // The ordinary field options stay closed for this row; a read-only explanation takes their place.
    expect(row!.querySelector('details')).toBeNull();
    expect(target.querySelector('[data-testid="notetype-field-meta-note-f_meta"]')).not.toBeNull();

    // The ordinary field above it keeps its editable name and its options.
    const text = target.querySelector('[data-testid="notetype-field-name-f_text"]') as HTMLInputElement;
    expect(text.readOnly).toBe(false);
    expect(target.querySelector('[data-testid="notetype-field-f_text"] details')).not.toBeNull();
  });

  it('saves the locked row unchanged instead of dropping or rewriting it', async () => {
    await setup();

    // Even a programmatic rename event and a move click cannot rewrite the built-in field.
    const name = target.querySelector('[data-testid="notetype-field-name-f_meta"]') as HTMLInputElement;
    name.value = 'renamed_meta';
    name.dispatchEvent(new Event('change', { bubbles: true }));
    await flush();
    (target.querySelector('[data-testid="notetype-field-up-f_meta"]') as HTMLButtonElement).click();
    await flush();

    const updateSpy = vi.spyOn(apiClient, 'updateNoteType');
    updateSpy.mockResolvedValueOnce({
      ...clozeNoteType(),
      dry_run: true,
      cards_added: 0,
      cards_removed: 0,
      reviews_on_removed_cards: 0,
    } as never);
    updateSpy.mockResolvedValueOnce({ ...clozeNoteType(), dry_run: false, version: 2 } as never);

    (target.querySelector('[data-testid="notetype-save"]') as HTMLButtonElement).click();
    await flush();

    expect(updateSpy).toHaveBeenCalledTimes(2);
    const savedFields = updateSpy.mock.calls[1]![2].config.fields;
    const metaFields = savedFields.filter((f) => f.name === 'engram_meta');
    expect(metaFields).toHaveLength(1);
    expect(metaFields[0]!.id).toBe('f_meta');
    expect(metaFields[0]!.description).toBe('managed by the server');
    expect(savedFields.findIndex((f) => f.name === 'engram_meta')).toBe(1);
    expect(document.body.querySelector('[data-testid="notetype-meta-delete-dialog"]')).toBeNull();
  });

  it('omits the metadata field from the answer-role binding choices', async () => {
    await setup();

    const trigger = target.querySelector('[data-testid="notetype-answer-binding-text"]') as HTMLElement;
    expect(trigger).not.toBeNull();
    trigger.click();
    await flush();

    const labels = Array.from(document.body.querySelectorAll('[role="option"]')).map(
      (el) => el.textContent?.trim() ?? '',
    );
    expect(labels).toContain('Text');
    expect(labels).not.toContain('engram_meta');
  });

  it('omits the metadata field from the change dialog rows but still maps it by name', async () => {
    await setup([clozeNoteType(), otherClozeType()]);

    (target.querySelector('[data-testid="notetype-change-open"]') as HTMLButtonElement).click();
    await flush();

    expect(document.body.querySelector('[data-testid="notetype-change-field-tg_text"]')).not.toBeNull();
    expect(document.body.querySelector('[data-testid="notetype-change-field-tg_meta"]')).toBeNull();

    // The source dropdown of the ordinary target field must not offer the metadata field, because
    // choosing it would copy the metadata JSON into a content field.
    const sourceTrigger = document.body.querySelector('[data-testid="notetype-change-field-tg_text"]') as HTMLElement;
    sourceTrigger.click();
    await flush();
    const sourceLabels = Array.from(document.body.querySelectorAll('[role="option"]')).map(
      (el) => el.textContent?.trim() ?? '',
    );
    expect(sourceLabels).toContain('Text');
    expect(sourceLabels).not.toContain('engram_meta');
    sourceTrigger.click();
    await flush();

    const changeSpy = vi.spyOn(apiClient, 'changeNoteType').mockResolvedValueOnce({
      dry_run: true,
      notes_moved: 0,
      cards_moved: 0,
      cards_added: 0,
      cards_removed: 0,
      reviews_on_removed_cards: 0,
    } as never);
    (document.body.querySelector('[data-testid="notetype-change-preview"]') as HTMLButtonElement).click();
    await flush();

    expect(changeSpy).toHaveBeenCalledTimes(1);
    expect(changeSpy.mock.calls[0]![2].target_note_type).toBe('nt2');
    // The same-name automatic mapping still carries the metadata field to the target type.
    expect(changeSpy.mock.calls[0]![2].field_mapping).toEqual({
      f_text: 'tg_text',
      f_meta: 'tg_meta',
    });
  });

  it('keeps the note editor alternatives editor for a cloze_blanks note type', async () => {
    setLocale('zh-CN');
    routeStore.set(matchRoute('/decks/d1/notes/new'));
    vi.spyOn(apiClient, 'listNoteTypes').mockResolvedValue({ note_types: [clozeNoteType()] } as never);
    vi.spyOn(apiClient, 'frameMediaBase').mockResolvedValue('');
    vi.spyOn(apiClient, 'previewNoteDraft').mockResolvedValue({ cards: [] } as never);

    target = document.createElement('div');
    document.body.appendChild(target);
    instance = mount(NoteEditView, { target });
    await flush();

    expect(target.querySelector('[data-testid="cloze-alternatives-editor"]')).not.toBeNull();
    // The metadata value is still edited as raw JSON, never as an ordinary rich-text field.
    expect(target.querySelector('[data-testid="note-field-engram_meta"]')).toBeNull();
  });

  it('displays localized error message when server rejects reserved prefix on save', async () => {
    await setup();
    const updateSpy = vi.spyOn(apiClient, 'updateNoteType').mockRejectedValueOnce(
      new ApiClientError('以 engram_ 开头的字段名已保留，engram_meta 除外。', {
        status: 400,
        code: 'invalid_request',
      })
    );

    const saveBtn = target.querySelector('[data-testid="notetype-save"]') as HTMLButtonElement;
    expect(saveBtn).not.toBeNull();
    saveBtn.click();
    await flush();

    expect(updateSpy).toHaveBeenCalled();
    const saveError = target.querySelector('[data-testid="notetype-save-error"]');
    expect(saveError).not.toBeNull();
    expect(saveError!.textContent).toBe('以 engram_ 开头的字段名已保留，engram_meta 除外。');
  });

  it.each([
    ['version_conflict', 409, 'notetype.error.version'],
    ['forbidden', 403, 'notetype.error.forbidden'],
  ])('shows the localized %s save failure without committing', async (code, status, key) => {
    await setup();
    const update = vi.spyOn(apiClient, 'updateNoteType').mockRejectedValueOnce(new ApiClientError('refused', { status, code }));
    (target.querySelector('[data-testid="notetype-save"]') as HTMLButtonElement).click();
    await flush();
    expect(update).toHaveBeenCalledTimes(1);
    expect(update.mock.calls[0]![2].dry_run).toBe(true);
    const { zhCN } = await import('../lib/i18n/locales/zh-CN');
    expect(target.querySelector('[data-testid="notetype-save-error"]')?.textContent).toBe(zhCN[key]);
  });
});
