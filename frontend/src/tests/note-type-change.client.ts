import { describe, it, expect, afterEach, vi } from 'vitest';
import { mount, unmount, flushSync } from 'svelte';
import NoteTypesView from '../lib/views/NoteTypesView.svelte';
import { routeStore, matchRoute } from '../lib/router';
import { ApiClientError, apiClient, type ChangeNoteTypeResult, type NoteType } from '../lib/api';
import { setLocale } from '../lib/i18n';

function noteType(overrides: Partial<NoteType> = {}): NoteType {
  return {
    id: 'nt-1',
    deck_id: 'd1',
    name: 'Basic',
    version: 4,
    config: {
      kind: 'normal',
      fields: [
        { id: 'f-front', name: 'Front', sticky: false, rtl: false, font: '', font_size: 0, description: '', plain_text: false, collapsed: false },
        { id: 'f-back', name: 'Back', sticky: false, rtl: false, font: '', font_size: 0, description: '', plain_text: false, collapsed: false },
      ],
      templates: [{ id: 't-1', name: 'Card 1', qfmt: '{{Front}}', afmt: '{{Back}}', answer: null }],
      css: '.card {}',
      sort_field_id: 'f-front',
    },
    created_at: '2026-01-01T00:00:00Z',
    updated_at: '2026-01-01T00:00:00Z',
    ...overrides,
  };
}

/** A second type of the same deck: different field and template ids under the same names. */
function targetType(): NoteType {
  return noteType({
    id: 'nt-2',
    name: 'Target',
    config: {
      kind: 'normal',
      fields: [
        { id: 'g-front', name: 'Front', sticky: false, rtl: false, font: '', font_size: 0, description: '', plain_text: false, collapsed: false },
        { id: 'g-back', name: 'Back', sticky: false, rtl: false, font: '', font_size: 0, description: '', plain_text: false, collapsed: false },
      ],
      templates: [{ id: 't-2', name: 'Card 1', qfmt: '{{Front}}', afmt: '{{Back}}', answer: null }],
      css: '.card {}',
      sort_field_id: 'g-front',
    },
  });
}

function changeResult(overrides: Partial<ChangeNoteTypeResult> = {}): ChangeNoteTypeResult {
  return {
    dry_run: true,
    notes_moved: 2,
    cards_moved: 3,
    cards_added: 1,
    cards_removed: 0,
    reviews_on_removed_cards: 0,
    ...overrides,
  };
}

describe('NoteTypesView empty-card cleanup and change note type', () => {
  let instance: ReturnType<typeof mount> | null = null;
  let target: HTMLElement;

  afterEach(() => {
    if (instance) unmount(instance);
    instance = null;
    document.body.innerHTML = '';
    vi.restoreAllMocks();
  });

  async function flush(): Promise<void> {
    for (let i = 0; i < 20; i++) await Promise.resolve();
    flushSync();
  }

  async function setup(types: NoteType[] = [noteType()]): Promise<void> {
    setLocale('zh-CN');
    routeStore.set(matchRoute('/decks/d1/note-types'));
    vi.spyOn(apiClient, 'listNoteTypes').mockResolvedValue({ note_types: types });
    target = document.createElement('div');
    document.body.appendChild(target);
    instance = mount(NoteTypesView, {
      target,
      props: {
        initialLoading: false,
        initialNoteTypes: types,
        initialStocks: [],
        initialControls: [],
      },
    });
    await flush();
  }

  function text(testId: string): string {
    return document.body.querySelector(`[data-testid="${testId}"]`)?.textContent?.trim() ?? '';
  }

  it('lists the empty cards, previews the removal, removes them and reloads the type list', async () => {
    await setup();
    const list = vi.spyOn(apiClient, 'listEmptyCards').mockResolvedValue({
      cards: [{ id: 'c1', note_id: 'n1', note_type_id: 'nt-1', template: 't-1', ordinal: 0 }],
      count: 1,
    });
    const cleanup = vi
      .spyOn(apiClient, 'cleanupEmptyCards')
      .mockResolvedValueOnce({ dry_run: true, cards_removed: 1, reviews_on_removed_cards: 2 })
      .mockResolvedValueOnce({ dry_run: false, cards_removed: 1, reviews_on_removed_cards: 2 });

    (document.body.querySelector('[data-testid="notetype-empty-open"]') as HTMLButtonElement).click();
    await flush();

    expect(list).toHaveBeenCalledWith('d1');
    expect(text('notetype-empty-count')).toContain('1');
    expect(text('notetype-empty-card-c1')).toContain('Basic');
    expect(text('notetype-empty-card-c1')).toContain('Card 1');

    // The removal stays disabled until the dry run reported its counts.
    expect((document.body.querySelector('[data-testid="notetype-empty-confirm"]') as HTMLButtonElement).disabled).toBe(true);

    (document.body.querySelector('[data-testid="notetype-empty-preview"]') as HTMLButtonElement).click();
    await flush();

    expect(cleanup).toHaveBeenCalledTimes(1);
    expect(cleanup.mock.calls[0]).toEqual(['d1', { dry_run: true }]);
    expect(text('notetype-empty-removed')).toBe('1');
    expect(text('notetype-empty-reviews')).toBe('2');

    (document.body.querySelector('[data-testid="notetype-empty-confirm"]') as HTMLButtonElement).click();
    await flush();

    expect(cleanup).toHaveBeenCalledTimes(2);
    expect(cleanup.mock.calls[1]).toEqual(['d1', { confirm_card_removal: true }]);
    expect(text('notetype-empty-removed')).toBe('1');
    expect(apiClient.listNoteTypes).toHaveBeenCalledTimes(1);
  });

  it('sends the prefilled field and template mapping of a change, shows the impact and writes it', async () => {
    await setup([noteType(), targetType()]);
    const change = vi
      .spyOn(apiClient, 'changeNoteType')
      .mockResolvedValueOnce(changeResult())
      .mockResolvedValueOnce(changeResult({ dry_run: false }));

    (document.body.querySelector('[data-testid="notetype-change-open"]') as HTMLButtonElement).click();
    await flush();

    (document.body.querySelector('[data-testid="notetype-change-preview"]') as HTMLButtonElement).click();
    await flush();

    expect(change).toHaveBeenCalledTimes(1);
    expect(change.mock.calls[0]).toEqual([
      'd1',
      'nt-1',
      {
        target_note_type: 'nt-2',
        field_mapping: { 'f-front': 'g-front', 'f-back': 'g-back' },
        template_mapping: { 't-1': 't-2' },
        dry_run: true,
        confirm_card_removal: false,
      },
    ]);
    expect(text('notetype-change-notes')).toBe('2');
    expect(text('notetype-change-moved')).toBe('3');
    expect(text('notetype-change-added')).toBe('1');
    expect(text('notetype-change-removed')).toBe('0');

    (document.body.querySelector('[data-testid="notetype-change-submit"]') as HTMLButtonElement).click();
    await flush();

    expect(change).toHaveBeenCalledTimes(2);
    expect(change.mock.calls[1]?.[2]).toMatchObject({ dry_run: false, confirm_card_removal: false });
    expect(apiClient.listNoteTypes).toHaveBeenCalledTimes(1);
  });

  it('asks for the extra confirmation when the server reports cards would be removed', async () => {
    await setup([noteType(), targetType()]);
    const change = vi
      .spyOn(apiClient, 'changeNoteType')
      .mockResolvedValueOnce(changeResult())
      .mockRejectedValueOnce(
        new ApiClientError('unconfirmed', {
          status: 409,
          code: 'card_removal_unconfirmed',
          details: {
            notes_moved: 2,
            cards_moved: 2,
            cards_added: 0,
            cards_removed: 1,
            reviews_on_removed_cards: 4,
          },
        }),
      )
      .mockResolvedValueOnce(changeResult({ dry_run: false, cards_removed: 1, reviews_on_removed_cards: 4 }));

    (document.body.querySelector('[data-testid="notetype-change-open"]') as HTMLButtonElement).click();
    await flush();
    (document.body.querySelector('[data-testid="notetype-change-preview"]') as HTMLButtonElement).click();
    await flush();
    (document.body.querySelector('[data-testid="notetype-change-submit"]') as HTMLButtonElement).click();
    await flush();

    expect(change).toHaveBeenCalledTimes(2);
    expect(text('notetype-change-added')).toBe('0');
    expect(text('notetype-change-removed')).toBe('1');
    expect(text('notetype-change-reviews')).toBe('4');
    expect(document.body.querySelector('[data-testid="notetype-change-confirm-dialog"]')).not.toBeNull();

    (document.body.querySelector('[data-testid="notetype-change-confirm-removal"]') as HTMLButtonElement).click();
    await flush();

    expect(change).toHaveBeenCalledTimes(3);
    expect(change.mock.calls[2]?.[2]).toMatchObject({
      dry_run: false,
      confirm_card_removal: true,
    });
  });
});
