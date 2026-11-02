import { describe, it, expect, beforeEach, afterEach, vi } from 'vitest';
import { mount, unmount, flushSync } from 'svelte';
import NoteTypesView from '../lib/views/NoteTypesView.svelte';
import { ApiClientError, type NoteType } from '../lib/api';
import { setLocale } from '../lib/i18n';
import { routeStore, matchRoute } from '../lib/router';
import { apiClient } from '../lib/api';

describe('NoteTypesView cloze alternatives & engram_meta management', () => {
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

  function clozeNoteType(): NoteType {
    return {
      id: 'nt1',
      deck_id: 'd1',
      name: 'Cloze Type',
      version: 1,
      config: {
        kind: 'cloze',
        fields: [
          {
            id: 'f1',
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
        ],
        templates: [
          {
            id: 't1',
            name: 'Cloze',
            qfmt: '{{cloze:Text}}',
            afmt: '{{cloze:Text}}',
            answer: {
              control: 'cloze_blanks',
              bindings: { text: 'f1' },
            },
          },
        ],
        css: '',
      },
      created_at: '2026-01-01T00:00:00Z',
      updated_at: '2026-01-01T00:00:00Z',
    };
  }

  async function setup(initialType = clozeNoteType()) {
    setLocale('zh-CN');
    routeStore.set(matchRoute('/decks/d1/note-types'));
    target = document.createElement('div');
    document.body.appendChild(target);
    instance = mount(NoteTypesView, {
      target,
      props: {
        initialLoading: false,
        initialNoteTypes: [initialType],
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

  it('shows enablement only for a cloze_blanks answer control', async () => {
    const type = clozeNoteType();
    type.config.templates[0]!.answer = undefined;
    await setup(type);
    expect(target.querySelector('[data-testid="notetype-cloze-alternatives"]')).toBeNull();
  });

  it('treats a manually added engram_meta field as enabled and excludes it from search on save', async () => {
    await setup();
    (target.querySelector('[data-testid="notetype-field-add"]') as HTMLButtonElement).click();
    await flush();
    const inputs = target.querySelectorAll<HTMLInputElement>('input[data-testid^="notetype-field-name-"]');
    const added = inputs[inputs.length - 1]!;
    added.value = 'engram_meta';
    added.dispatchEvent(new Event('change', { bubbles: true }));
    await flush();
    expect(target.querySelector('[data-testid="notetype-cloze-alternatives-status"]')).not.toBeNull();
    expect(target.querySelector('[data-testid="notetype-cloze-alternatives-enable"]')).toBeNull();
    const update = vi.spyOn(apiClient, 'updateNoteType');
    update.mockResolvedValueOnce({ ...clozeNoteType(), cards_added: 0, cards_removed: 0, reviews_on_removed_cards: 0 } as any);
    update.mockResolvedValueOnce({ ...clozeNoteType(), version: 2 } as any);
    (target.querySelector('[data-testid="notetype-save"]') as HTMLButtonElement).click();
    await flush();
    const fields = update.mock.calls[0]![2].config.fields.filter(f => f.name === 'engram_meta');
    expect(fields).toHaveLength(1);
    expect(fields[0]!.exclude_from_search).toBe(true);
  });

  it('uses the existing impact confirmation for enablement combined with template changes', async () => {
    await setup();
    (target.querySelector('[data-testid="notetype-cloze-alternatives-enable"]') as HTMLButtonElement).click();
    const qfmt = target.querySelector('[data-testid="notetype-template-qfmt"]') as HTMLTextAreaElement;
    qfmt.value = '{{cloze:Text}} changed';
    qfmt.dispatchEvent(new Event('input', { bubbles: true }));
    await flush();
    const update = vi.spyOn(apiClient, 'updateNoteType');
    update.mockResolvedValueOnce({ ...clozeNoteType(), cards_added: 2, cards_removed: 1, reviews_on_removed_cards: 3 } as any);
    update.mockResolvedValueOnce({ ...clozeNoteType(), version: 2 } as any);
    (target.querySelector('[data-testid="notetype-save"]') as HTMLButtonElement).click();
    await flush();
    expect(update).toHaveBeenCalledTimes(1);
    expect(update.mock.calls[0]![2].config.templates[0]!.qfmt).toBe('{{cloze:Text}} changed');
    expect(document.body.querySelector('[data-testid="notetype-impact-added"]')?.textContent).toBe('2');
    expect(document.body.querySelector('[data-testid="notetype-impact-removed"]')?.textContent).toBe('1');
    (document.body.querySelector('[data-testid="notetype-impact-confirm"]') as HTMLButtonElement).click();
    await flush();
    expect(update).toHaveBeenCalledTimes(2);
    expect(update.mock.calls[1]![2]).toMatchObject({ dry_run: false, confirm_card_removal: true });
  });

  it('enables cloze alternatives by adding engram_meta, saves with dry_run 0/0 and no new impact dialog, setting exclude_from_search flag', async () => {
    const { target } = await setup();

    const altSection = target.querySelector('[data-testid="notetype-cloze-alternatives"]');
    expect(altSection).not.toBeNull();

    const enableBtn = target.querySelector('[data-testid="notetype-cloze-alternatives-enable"]') as HTMLButtonElement;
    expect(enableBtn).not.toBeNull();
    enableBtn.click();
    await flush();

    // Status badge is now shown as "已配置"
    const status = target.querySelector('[data-testid="notetype-cloze-alternatives-status"]');
    expect(status).not.toBeNull();
    expect(status!.textContent).toContain('已配置');

    // Field list now contains engram_meta
    const fieldNames = Array.from(target.querySelectorAll('input[data-testid^="notetype-field-name-"]')).map(
      (el) => (el as HTMLInputElement).value
    );
    expect(fieldNames).toContain('engram_meta');

    // Mock updateNoteType for dry_run and real save
    const updateSpy = vi.spyOn(apiClient, 'updateNoteType');
    // First call is dry run with 0 added / 0 removed
    updateSpy.mockResolvedValueOnce({
      ...clozeNoteType(),
      dry_run: true,
      cards_added: 0,
      cards_removed: 0,
      reviews_on_removed_cards: 0,
    } as any);
    // Second call is real save
    updateSpy.mockResolvedValueOnce({
      ...clozeNoteType(),
      dry_run: false,
      version: 2,
    } as any);

    const saveBtn = target.querySelector('[data-testid="notetype-save"]') as HTMLButtonElement;
    expect(saveBtn).not.toBeNull();
    saveBtn.click();
    await flush();

    // Assert update was called twice (dry run then commit)
    expect(updateSpy).toHaveBeenCalledTimes(2);
    expect(updateSpy.mock.calls[0]?.[2]).toMatchObject({ dry_run: true });
    expect(updateSpy.mock.calls[1]?.[2]).toMatchObject({ dry_run: false, confirm_card_removal: false });

    // Assert no new impact dialog was opened
    expect(document.body.querySelector('[data-testid="notetype-impact"]')).toBeNull();

    // Assert the saved config has the engram_meta field with exclude_from_search set to true (exclude flag)
    const savedFields = updateSpy.mock.calls[1]![2].config.fields;
    const metaField = savedFields.find((f: any) => f.name === 'engram_meta');
    expect(metaField).toBeDefined();
    expect(metaField!.exclude_from_search).toBe(true);
    expect(metaField!.plain_text).toBe(true);
  });

  it('reuses existing field without replacing its ID or values when engram_meta already exists (reuseID)', async () => {
    const existingType = clozeNoteType();
    existingType.config.fields.push({
      id: 'f_legacy_meta',
      name: 'engram_meta',
      sticky: false,
      rtl: false,
      font: '',
      font_size: 0,
      description: 'legacy description to keep',
      plain_text: false,
      collapsed: true,
      exclude_from_search: false, // not yet excluded
      tag: null,
      prevent_deletion: false,
    });

    const { target } = await setup(existingType);

    // Field presence enables reading; fixing its search flag must retain its ID.
    expect(target.querySelector('[data-testid="notetype-cloze-alternatives-enable"]')).toBeNull();
    const fixFlagBtn = target.querySelector('[data-testid="notetype-cloze-alternatives-fix-flag"]') as HTMLButtonElement;
    expect(fixFlagBtn).not.toBeNull();
    fixFlagBtn.click();
    await flush();

    // Status is now "已配置"
    const status = target.querySelector('[data-testid="notetype-cloze-alternatives-status"]');
    expect(status).not.toBeNull();
    expect(status!.textContent).toContain('已配置');

    // Mock updateNoteType for save
    const updateSpy = vi.spyOn(apiClient, 'updateNoteType');
    updateSpy.mockResolvedValueOnce({
      ...existingType,
      dry_run: true,
      cards_added: 0,
      cards_removed: 0,
      reviews_on_removed_cards: 0,
    } as any);
    updateSpy.mockResolvedValueOnce({
      ...existingType,
      dry_run: false,
      version: 2,
    } as any);

    const saveBtn = target.querySelector('[data-testid="notetype-save"]') as HTMLButtonElement;
    expect(saveBtn).not.toBeNull();
    saveBtn.click();
    await flush();

    // Assert save without impact dialog
    expect(document.body.querySelector('[data-testid="notetype-impact"]')).toBeNull();

    // Assert saved fields list: exactly one engram_meta field, reusing existing ID and preserving description
    const savedFields = updateSpy.mock.calls[1]![2].config.fields;
    const metaFields = savedFields.filter((f: any) => f.name === 'engram_meta');
    expect(metaFields).toHaveLength(1);
    expect(metaFields[0]!.id).toBe('f_legacy_meta'); // Reused ID!
    expect(metaFields[0]!.description).toBe('legacy description to keep'); // Preserved values!
    expect(metaFields[0]!.exclude_from_search).toBe(true); // Exclude flag updated!
  });

  it('shows warning dialog when attempting to delete engram_meta from field list, cancels safely or confirms deletion', async () => {
    const typeWithMeta = clozeNoteType();
    typeWithMeta.config.fields.push({
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
    });

    const { target } = await setup(typeWithMeta);

    // Status badge shows configured
    const status = target.querySelector('[data-testid="notetype-cloze-alternatives-status"]');
    expect(status).not.toBeNull();
    expect(status!.textContent).toContain('已配置');

    // Click delete trash button on the engram_meta field item in fields list
    const fieldDeleteBtn = target.querySelector('[data-testid="notetype-field-delete-f_meta"]') as HTMLButtonElement;
    expect(fieldDeleteBtn).not.toBeNull();
    fieldDeleteBtn.click();
    await flush();

    // Dialog is open
    const dialog = document.body.querySelector('[data-testid="notetype-meta-delete-dialog"]');
    expect(dialog).not.toBeNull();
    expect(dialog!.textContent).toContain('后续保存该类型下的笔记会丢弃所有元数据值，导出不再携带');

    // 1. Cancel deletion
    const cancelBtn = document.body.querySelector('[data-testid="notetype-meta-delete-cancel"]') as HTMLButtonElement;
    expect(cancelBtn).not.toBeNull();
    cancelBtn.click();
    await flush();

    // Field is still present
    let fieldNames = Array.from(target.querySelectorAll('input[data-testid^="notetype-field-name-"]')).map(
      (el) => (el as HTMLInputElement).value
    );
    expect(fieldNames).toContain('engram_meta');

    // 2. Click delete again and confirm
    fieldDeleteBtn.click();
    await flush();
    const confirmBtn = document.body.querySelector('[data-testid="notetype-meta-delete-confirm"]') as HTMLButtonElement;
    expect(confirmBtn).not.toBeNull();
    confirmBtn.click();
    await flush();

    // Field is now deleted
    fieldNames = Array.from(target.querySelectorAll('input[data-testid^="notetype-field-name-"]')).map(
      (el) => (el as HTMLInputElement).value
    );
    expect(fieldNames).not.toContain('engram_meta');
  });

  it('displays localized error message when server rejects reserved prefix on save', async () => {
    const { target } = await setup();
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
