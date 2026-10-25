import { describe, it, expect, afterEach, vi } from 'vitest';
import { mount, unmount, flushSync } from 'svelte';
import DecksView from '../lib/views/DecksView.svelte';
import { apiClient } from '../lib/api';
import type { Deck } from '../lib/api';
import { setLocale } from '../lib/i18n';

/**
 * Bulk delete from the deck list: the selection bar opens a confirmation dialog, the request carries every
 * selected deck id (the server decides ownership per deck), and decks the server reports as skipped stay in the list.
 */
describe('DecksView batch delete', () => {
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

  function deck(id: string, name: string, role: Deck['role'] = 'owner'): Deck {
    return {
      id,
      name,
      description: '',
      new_per_day: 20,
      reviews_per_day: 200,
      preset_id: 'p1',
      created_at: '',
      role,
    };
  }

  async function setup(decks: Deck[]): Promise<ReturnType<typeof vi.spyOn>> {
    setLocale('zh-CN');
    vi.spyOn(apiClient, 'getDecks').mockResolvedValue({ decks });
    vi.spyOn(apiClient, 'getDeckQueueCounts').mockResolvedValue({ decks: [] } as never);
    vi.spyOn(apiClient, 'getShareInvites').mockResolvedValue({ invites: [] } as never);
    const bulkDecks = vi.spyOn(apiClient, 'bulkDecks');
    target = document.createElement('div');
    document.body.appendChild(target);
    instance = mount(DecksView, { target });
    await flush();
    return bulkDecks;
  }

  async function click(selector: string): Promise<void> {
    const el = document.body.querySelector(selector) as HTMLElement | null;
    expect(el, selector).not.toBeNull();
    el!.click();
    await flush();
  }

  function dialog(): Element | null {
    return document.body.querySelector('[data-testid="decks-batch-delete-dialog"]');
  }

  it('confirms before deleting, sends every selected id once, and removes the rows', async () => {
    const bulkDecks = await setup([deck('d1', 'Alpha'), deck('d2', 'Beta')]);
    bulkDecks.mockResolvedValue({ dry_run: false, affected: 2, skipped: [] });

    await click('[aria-label="选择卡组 Alpha"]');
    await click('[aria-label="选择卡组 Beta"]');
    await click('[data-testid="decks-batch-delete"]');

    // Nothing is sent before the user confirms.
    expect(bulkDecks).not.toHaveBeenCalled();
    expect(dialog()).not.toBeNull();
    expect(dialog()!.textContent).toContain('确定删除 2 个卡组？');

    await click('[data-testid="decks-batch-delete-confirm"]');

    expect(bulkDecks).toHaveBeenCalledTimes(1);
    expect(bulkDecks).toHaveBeenCalledWith({ action: 'delete', deck_ids: ['d1', 'd2'], dry_run: false });
    expect(document.body.querySelector('[data-testid="deck-card-d1"]')).toBeNull();
    expect(document.body.querySelector('[data-testid="deck-card-d2"]')).toBeNull();
    // The selection is cleared, so the selection bar disappears.
    expect(document.body.querySelector('[data-testid="decks-batch-delete"]')).toBeNull();
    expect(dialog()?.getAttribute('data-state')).not.toBe('open');
  });

  it('warns about shared decks and keeps them when the server skips them', async () => {
    const bulkDecks = await setup([deck('d1', 'Alpha'), deck('d2', 'Beta', 'editor')]);
    bulkDecks.mockResolvedValue({ dry_run: false, affected: 1, skipped: [{ deck_id: 'd2', code: 'forbidden' }] });

    await click('[aria-label="选择卡组 Alpha"]');
    await click('[aria-label="选择卡组 Beta"]');
    await click('[data-testid="decks-batch-delete"]');

    const hint = document.body.querySelector('[data-testid="decks-batch-delete-shared-hint"]');
    expect(hint?.textContent).toContain('有 1 个是共享给你的');

    await click('[data-testid="decks-batch-delete-confirm"]');

    // The server is the authority: the shared deck is kept even though the client sent its id.
    expect(bulkDecks).toHaveBeenCalledWith({ action: 'delete', deck_ids: ['d1', 'd2'], dry_run: false });
    expect(document.body.querySelector('[data-testid="deck-card-d1"]')).toBeNull();
    expect(document.body.querySelector('[data-testid="deck-card-d2"]')).not.toBeNull();
  });

  it('keeps the dialog open with the error and leaves the list untouched when the request fails', async () => {
    const bulkDecks = await setup([deck('d1', 'Alpha'), deck('d2', 'Beta')]);
    bulkDecks.mockRejectedValueOnce(new Error('boom'));

    await click('[aria-label="选择卡组 Alpha"]');
    await click('[aria-label="选择卡组 Beta"]');
    await click('[data-testid="decks-batch-delete"]');
    await click('[data-testid="decks-batch-delete-confirm"]');

    expect(dialog()?.getAttribute('data-state')).toBe('open');
    expect(dialog()!.querySelector('[role="alert"]')?.textContent).toContain('批量删除失败');
    expect(document.body.querySelector('[data-testid="deck-card-d1"]')).not.toBeNull();
    expect(document.body.querySelector('[data-testid="deck-card-d2"]')).not.toBeNull();
  });
});
