import { describe, it, expect, afterEach, vi } from 'vitest';
import { mount, unmount, flushSync } from 'svelte';
import NoteTypesView from '../lib/views/NoteTypesView.svelte';
import { routeStore, matchRoute } from '../lib/router';
import { apiClient, type NoteType, type UpdateNoteTypeResponse } from '../lib/api';
import { setLocale } from '../lib/i18n';

function noteType(): NoteType {
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
  };
}

function impactResponse(added: number, removed: number, reviews: number): UpdateNoteTypeResponse {
  return {
    ...noteType(),
    dry_run: true,
    cards_added: added,
    cards_removed: removed,
    reviews_on_removed_cards: reviews,
  };
}

describe('NoteTypesView editor', () => {
  let instance: ReturnType<typeof mount> | null = null;
  let target: HTMLElement;

  afterEach(() => {
    if (instance) unmount(instance);
    instance = null;
    document.body.innerHTML = '';
    vi.useRealTimers();
    vi.restoreAllMocks();
  });

  async function flush(): Promise<void> {
    for (let i = 0; i < 20; i++) await Promise.resolve();
    flushSync();
  }

  async function setup(): Promise<ReturnType<typeof vi.fn>> {
    setLocale('zh-CN');
    routeStore.set(matchRoute('/decks/d1/note-types'));
    const update = vi.spyOn(apiClient, 'updateNoteType');
    target = document.createElement('div');
    document.body.appendChild(target);
    instance = mount(NoteTypesView, {
      target,
      props: {
        initialLoading: false,
        initialNoteTypes: [noteType()],
        initialStocks: [],
        initialControls: [],
      },
    });
    await flush();
    return update as unknown as ReturnType<typeof vi.fn>;
  }

  it('shows the counts the server returns before saving a card-changing edit', async () => {
    const update = await setup();
    update.mockResolvedValueOnce(impactResponse(2, 1, 5));
    update.mockResolvedValueOnce({ ...impactResponse(2, 1, 5), dry_run: false, version: 5 });

    const save = document.body.querySelector('[data-testid="notetype-save"]') as HTMLButtonElement | null;
    expect(save).not.toBeNull();
    save!.click();
    await flush();

    // First request is the dry run; nothing has been written yet.
    expect(update).toHaveBeenCalledTimes(1);
    expect(update.mock.calls[0]?.[2]).toMatchObject({ dry_run: true, version: 4 });
    const impact = document.body.querySelector('[data-testid="notetype-impact"]');
    expect(impact?.textContent).toContain('2');
    expect(impact?.textContent).toContain('1');
    expect(impact?.textContent).toContain('5');
    expect(document.body.querySelector('[data-testid="notetype-impact-added"]')?.textContent).toBe('2');
    expect(document.body.querySelector('[data-testid="notetype-impact-removed"]')?.textContent).toBe('1');
    expect(document.body.querySelector('[data-testid="notetype-impact-reviews"]')?.textContent).toBe('5');

    const confirm = document.body.querySelector('[data-testid="notetype-impact-confirm"]') as HTMLButtonElement | null;
    expect(confirm).not.toBeNull();
    confirm!.click();
    await flush();

    expect(update).toHaveBeenCalledTimes(2);
    expect(update.mock.calls[1]?.[2]).toMatchObject({
      dry_run: false,
      confirm_card_removal: true,
      version: 4,
    });
  });

  it('saves immediately when the dry run reports no card impact', async () => {
    const update = await setup();
    update.mockResolvedValueOnce(impactResponse(0, 0, 0));
    update.mockResolvedValueOnce({ ...impactResponse(0, 0, 0), dry_run: false, version: 5 });

    const save = document.body.querySelector('[data-testid="notetype-save"]') as HTMLButtonElement | null;
    save!.click();
    await flush();

    expect(update).toHaveBeenCalledTimes(2);
    expect(update.mock.calls[1]?.[2]).toMatchObject({ dry_run: false, confirm_card_removal: false });
    expect(document.body.querySelector('[data-testid="notetype-impact"]')).toBeNull();
  });

  it('sends the unsaved config to the server preview and shows the returned HTML in the frame', async () => {
    vi.useFakeTimers();
    const preview = vi.spyOn(apiClient, 'previewNoteType').mockResolvedValue({
      front: {
        html: '<div id="server-front">SERVER-FRONT</div>',
        css: '.card {}',
        body_class: 'card',
        side: 'front',
        media_base: '',
      },
      back: {
        html: '<div id="server-back">SERVER-BACK</div>',
        css: '.card {}',
        body_class: 'card',
        side: 'back',
        media_base: '',
      },
    });
    setLocale('zh-CN');
    routeStore.set(matchRoute('/decks/d1/note-types'));
    target = document.createElement('div');
    document.body.appendChild(target);
    instance = mount(NoteTypesView, {
      target,
      props: {
        initialLoading: false,
        initialNoteTypes: [noteType()],
        initialStocks: [],
        initialControls: [],
      },
    });
    await flush();
    await vi.advanceTimersByTimeAsync(300);
    await flush();
    // The mount already previewed the initial draft; the edit below is the request this test asserts.
    preview.mockClear();

    const qfmt = document.body.querySelector('[data-testid="notetype-template-qfmt"]') as HTMLTextAreaElement | null;
    expect(qfmt).not.toBeNull();
    qfmt!.value = '{{Front}} EDITED';
    qfmt!.dispatchEvent(new Event('input', { bubbles: true }));
    await flush();
    await vi.advanceTimersByTimeAsync(300);
    await flush();

    expect(preview).toHaveBeenCalledTimes(1);
    const [deck, config, templateId, options] = preview.mock.calls[0]!;
    expect(deck).toBe('d1');
    expect(config.templates[0]?.qfmt).toBe('{{Front}} EDITED');
    expect(templateId).toBe('t-1');
    expect(options).toEqual({ frame: true });

    // happy-dom mounts the iframe without loading the frame document, so the test answers the frame protocol the way
    // the real frame does and reads what CardFrame sends into it.
    const element = document.body.querySelector(
      '[data-testid="notetype-preview-front"] iframe[data-card-frame]',
    ) as HTMLIFrameElement | null;
    expect(element).not.toBeNull();
    const win = element!.contentWindow!;
    vi.spyOn(win, 'postMessage').mockImplementation(() => {});
    window.dispatchEvent(new MessageEvent('message', { data: { type: 'ready' }, source: win }));
    await flush();
    const shows = ((win.postMessage as unknown as { mock: { calls: unknown[][] } }).mock.calls ?? [])
      .map((call) => call[0] as Record<string, unknown>)
      .filter((message) => message.type === 'show');
    expect(shows.at(-1)?.html).toContain('SERVER-FRONT');
  });
});
