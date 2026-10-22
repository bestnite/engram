import { describe, it, expect, afterEach, vi } from 'vitest';
import { mount, unmount, flushSync } from 'svelte';
import DecksView from '../lib/views/DecksView.svelte';
import { apiClient } from '../lib/api';
import type { Deck } from '../lib/api';
import { setLocale } from '../lib/i18n';

/**
 * Batch export from the deck list: clicking "Export" first opens the same options
 * dialog as single-deck export, and the user's media / progress / review log / weights
 * choices are passed unchanged to the batch export endpoint.
 */
describe('DecksView batch export', () => {
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

  function deck(id: string, name: string): Deck {
    return {
      id,
      name,
      description: '',
      new_per_day: 20,
      reviews_per_day: 200,
      preset_id: 'p1',
      created_at: '',
      role: 'owner',
    };
  }

  async function setup(): Promise<ReturnType<typeof vi.spyOn>> {
    setLocale('zh-CN');
    vi.spyOn(apiClient, 'getDecks').mockResolvedValue({
      decks: [deck('d1', 'Alpha'), deck('d2', 'Beta')],
    });
    vi.spyOn(apiClient, 'getDeckQueueCounts').mockResolvedValue({ decks: [] } as never);
    vi.spyOn(apiClient, 'getShareInvites').mockResolvedValue({ invites: [] } as never);
    const exportZip = vi.spyOn(apiClient, 'exportDecksZip').mockResolvedValue({ blob: new Blob(['zip']), filename: 'decks.zip' });
    vi.spyOn(URL, 'createObjectURL').mockReturnValue('blob:mock');
    vi.spyOn(URL, 'revokeObjectURL').mockImplementation(() => {});
    target = document.createElement('div');
    document.body.appendChild(target);
    instance = mount(DecksView, { target });
    await flush();
    return exportZip;
  }

  async function click(selector: string): Promise<void> {
    const el = document.body.querySelector(selector) as HTMLElement | null;
    expect(el, selector).not.toBeNull();
    el!.click();
    await flush();
  }

  async function selectAndOpen(): Promise<void> {
    await click('[aria-label="选择卡组 Alpha"]');
    await click('[aria-label="选择卡组 Beta"]');
    await click('[data-testid="decks-batch-export"]');
  }

  it('opens the options dialog instead of downloading at once', async () => {
    const exportZip = await setup();
    await selectAndOpen();
    expect(document.body.querySelector('[data-testid="decks-batch-export-dialog"]')).not.toBeNull();
    expect(document.body.querySelector('[data-testid="export-include-media"]')).not.toBeNull();
    expect(document.body.querySelector('[data-testid="export-include-progress"]')).not.toBeNull();
    expect(document.body.querySelector('[data-testid="export-include-weights"]')).not.toBeNull();
    expect(exportZip).not.toHaveBeenCalled();
  });

  it('exports with the default options when nothing is changed', async () => {
    const exportZip = await setup();
    await selectAndOpen();
    await click('[data-testid="decks-batch-export-submit"]');
    expect(exportZip).toHaveBeenCalledWith(['d1', 'd2'], {
      includeMedia: true,
      includeProgress: false,
      includeReviews: false,
      includeWeights: false,
    });
    // The node is still present during the exit animation; closing shows up as data-state turning closed.
    expect(document.body.querySelector('[data-testid="decks-batch-export-dialog"]')?.getAttribute('data-state')).not.toBe('open');
  });

  it('passes the chosen options to the batch export', async () => {
    const exportZip = await setup();
    await selectAndOpen();
    await click('[data-testid="export-include-media"]');
    await click('[data-testid="export-include-progress"]');
    await click('[data-testid="export-include-reviews"]');
    await click('[data-testid="export-include-weights"]');
    await click('[data-testid="decks-batch-export-submit"]');
    expect(exportZip).toHaveBeenCalledWith(['d1', 'd2'], {
      includeMedia: false,
      includeProgress: true,
      includeReviews: true,
      includeWeights: true,
    });
  });

  it('drops review history when progress is unchecked again', async () => {
    // The server rejects "review log without progress", so ticking both and then unticking progress must not submit that combination.
    const exportZip = await setup();
    await selectAndOpen();
    await click('[data-testid="export-include-progress"]');
    await click('[data-testid="export-include-reviews"]');
    await click('[data-testid="export-include-progress"]');
    expect(document.body.querySelector('[data-testid="export-include-reviews"]')).toBeNull();
    await click('[data-testid="decks-batch-export-submit"]');
    expect(exportZip).toHaveBeenCalledWith(['d1', 'd2'], expect.objectContaining({ includeProgress: false, includeReviews: false }));
  });

  it('keeps the dialog open and shows an error when the export fails', async () => {
    const exportZip = await setup();
    exportZip.mockRejectedValueOnce(new Error('boom'));
    await selectAndOpen();
    await click('[data-testid="decks-batch-export-submit"]');
    const dialog = document.body.querySelector('[data-testid="decks-batch-export-dialog"]');
    expect(dialog?.getAttribute('data-state')).toBe('open');
    expect(dialog!.querySelector('[role="alert"]')?.textContent).toContain('批量导出失败');
  });
});
