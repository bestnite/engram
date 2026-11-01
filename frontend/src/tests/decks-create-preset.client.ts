import { describe, it, expect, afterEach, vi } from 'vitest';
import { mount, unmount, flushSync } from 'svelte';
import DecksView from '../lib/views/DecksView.svelte';
import { apiClient } from '../lib/api';
import { setLocale } from '../lib/i18n';

/**
 * A failed preset read in the create-deck dialog used to assign a fresh empty list, which re-triggered
 * the effect that read that list and started the request again. One failure must leave one request and
 * one message behind, not a request loop.
 */
describe('DecksView create dialog preset load', () => {
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

  async function setup(): Promise<ReturnType<typeof vi.spyOn>> {
    setLocale('en');
    vi.spyOn(apiClient, 'getDecks').mockResolvedValue({ decks: [] });
    vi.spyOn(apiClient, 'getDeckQueueCounts').mockResolvedValue({ decks: [] } as never);
    vi.spyOn(apiClient, 'getShareInvites').mockResolvedValue({ invites: [] } as never);
    const listPresets = vi.spyOn(apiClient, 'listPresets').mockRejectedValue(new Error('offline'));
    target = document.createElement('div');
    document.body.appendChild(target);
    instance = mount(DecksView, { target });
    await flush();
    return listPresets;
  }

  it('requests the preset list once and reports the failure in the dialog', async () => {
    const listPresets = await setup();

    (document.body.querySelector('[data-testid="deck-create-open"]') as HTMLElement).click();
    for (let round = 0; round < 6; round++) await flush();

    expect(listPresets).toHaveBeenCalledTimes(1);
    expect(document.body.querySelector('[data-testid="deck-create-preset-error"]')).not.toBeNull();
  });
});
