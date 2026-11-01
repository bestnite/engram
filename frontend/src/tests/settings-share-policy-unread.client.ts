import { describe, it, expect, afterEach, vi } from 'vitest';
import { mount, unmount, flushSync } from 'svelte';
import SettingsView from '../lib/views/SettingsView.svelte';
import { apiClient, ApiClientError } from '../lib/api';
import type { UserProfile } from '../lib/api';
import { setLocale, formatMessage } from '../lib/i18n';

/**
 * A failed read of the deck-invitation policy must not be shown as "Anyone". The stored value is unknown, so the
 * policy control is withheld and a retry is offered instead; only a successful read may render the dropdown.
 */
describe('SettingsView share policy read failure', () => {
  let instance: ReturnType<typeof mount> | null = null;
  let target: HTMLElement;

  const profile: UserProfile = {
    display_name: 'nite',
    locale: 'en',
    timezone: 'UTC',
    day_cutoff_hour: 4,
    learn_ahead_minutes: 0,
  };

  afterEach(() => {
    if (instance) unmount(instance);
    instance = null;
    document.body.innerHTML = '';
    vi.restoreAllMocks();
    vi.clearAllMocks();
  });

  async function flush(): Promise<void> {
    for (let i = 0; i < 20; i++) await Promise.resolve();
    flushSync();
  }

  async function setup(failFirstRead: boolean): Promise<ReturnType<typeof vi.spyOn>> {
    setLocale('en');
    vi.spyOn(apiClient, 'getProfile').mockResolvedValue(profile);
    const read = vi.spyOn(apiClient, 'getShareInvites');
    if (failFirstRead) {
      read.mockRejectedValueOnce(new ApiClientError('HTTP 500: internal', { status: 500, code: 'internal' }));
    }
    target = document.createElement('div');
    document.body.appendChild(target);
    instance = mount(SettingsView, { target, props: { initialLoading: false } });
    await flush();
    return read as unknown as ReturnType<typeof vi.spyOn>;
  }

  it('shows no policy option when the read fails, then renders the stored value after a retry', async () => {
    const read = await setup(true);

    expect(document.body.querySelector('[data-testid="share-policy-options"]')).toBeNull();
    expect(document.body.querySelector('[data-testid="share-policy-unread"]')?.textContent ?? '').toContain(
      formatMessage('en', 'settings.share_policy.unread')
    );
    expect(document.body.textContent).not.toContain(formatMessage('en', 'settings.share_policy.anyone'));

    // The retry button must issue a second read and render the value the server then reports.
    read.mockResolvedValueOnce({ policy: 'nobody', allow_list: [] } as never);
    const retry = document.body.querySelector('[data-testid="share-policy-retry"]') as HTMLButtonElement | null;
    expect(retry).not.toBeNull();
    retry!.click();
    await flush();

    expect(read).toHaveBeenCalledTimes(2);
    expect(document.body.querySelector('[data-testid="share-policy-unread"]')).toBeNull();
    const trigger = document.body.querySelector('[data-testid="share-policy-options"]');
    expect(trigger).not.toBeNull();
    expect(trigger?.textContent).toContain(formatMessage('en', 'settings.share_policy.nobody'));
  });
});
