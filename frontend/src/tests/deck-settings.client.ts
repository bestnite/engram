import { describe, it, expect, afterEach, vi } from 'vitest';
import { mount, unmount, flushSync } from 'svelte';
import DeckSettingsView from '../lib/views/DeckSettingsView.svelte';
import { apiClient, ApiClientError } from '../lib/api';
import type { DeckSettings } from '../lib/api';
import { routeStore, matchRoute } from '../lib/router';
import { setLocale, formatMessage } from '../lib/i18n';
import { toast } from '../lib/components/ui/toast';

vi.mock('../lib/components/ui/toast', () => ({ toast: { success: vi.fn(), warning: vi.fn(), error: vi.fn() } }));

/**
 * The daily-cap save path is only reached by a real mount: the input writes string state,
 * and if the binding coerced the value to number/null, parseCap's trim check would throw
 * and the PATCH request would never leave the browser.
 */
describe('DeckSettingsView cap saving', () => {
  let instance: ReturnType<typeof mount> | null = null;
  let target: HTMLElement;

  const settings: DeckSettings = {
    deck_id: 'd1',
    deck_name: 'Biology',
    deck_description: '',
    role: 'owner',
    preset_id: '3',
    autoplay: true,
    description_markdown: false,
    new_per_day: 10,
    reviews_per_day: 10,
    new_used: 2,
    new_extra: 0,
    review_used: 3,
    new_left: 8,
    review_left: 7,
    new_unlimited: false,
    review_unlimited: false,
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

  /** Same path as the browser: write value first, then dispatch input, so the binding takes the value into state. */
  async function type(testId: string, value: string): Promise<void> {
    const el = document.body.querySelector(`[data-testid="${testId}"]`) as HTMLInputElement | null;
    expect(el, testId).not.toBeNull();
    el!.value = value;
    el!.dispatchEvent(new Event('input', { bubbles: true }));
    await flush();
  }

  async function submit(): Promise<void> {
    const form = document.body.querySelector('[data-testid="deck-settings-form"]') as HTMLFormElement | null;
    expect(form).not.toBeNull();
    form!.dispatchEvent(new Event('submit', { bubbles: true, cancelable: true }));
    await flush();
  }

  async function setup(role: DeckSettings['role'] = 'owner'): Promise<ReturnType<typeof vi.fn>> {
    setLocale('zh-CN');
    routeStore.set(matchRoute('/decks/d1/settings'));
    vi.spyOn(apiClient, 'listPresets').mockResolvedValue({ presets: [] } as never);
    const update = vi
      .spyOn(apiClient, 'updateDeckSettings')
      .mockResolvedValue({ ...settings, role, new_per_day: 15, new_left: 13 });
    target = document.createElement('div');
    document.body.appendChild(target);
    instance = mount(DeckSettingsView, {
      target,
      props: { initialLoading: false, initialSettings: { ...settings, role } },
    });
    await flush();
    return update as unknown as ReturnType<typeof vi.fn>;
  }

  it('saves the typed caps instead of throwing out of the submit handler', async () => {
    const update = await setup();
    await type('deck-settings-new-per-day', '15');
    await submit();
    expect(update).toHaveBeenCalledWith('d1', {
      new_per_day: 15,
      reviews_per_day: 10,
      preset_id: '3',
      autoplay: true,
      description_markdown: false,
    });
    expect(toast.success).toHaveBeenCalledWith(formatMessage('zh-CN', 'deck.settings.saved'));
  });

  it('sends the description format together with the other deck-wide settings', async () => {
    const update = await setup();
    const markdown = document.body.querySelector('[data-testid="deck-settings-markdown"]') as HTMLElement | null;
    expect(markdown).not.toBeNull();
    markdown!.click();
    await flush();
    await submit();
    expect(update).toHaveBeenCalledWith('d1', {
      new_per_day: 10,
      reviews_per_day: 10,
      preset_id: '3',
      autoplay: true,
      description_markdown: true,
    });
  });

  it('keeps the deck-wide description format out of a sharing member\'s request', async () => {
    const update = await setup('reader');
    const markdown = document.body.querySelector('[data-testid="deck-settings-markdown"]') as HTMLElement | null;
    expect(markdown).not.toBeNull();
    expect(markdown!.hasAttribute('disabled') || markdown!.getAttribute('data-disabled') !== null).toBe(true);
    await submit();
    expect(update).toHaveBeenCalledWith('d1', {
      new_per_day: 10,
      reviews_per_day: 10,
      preset_id: '3',
    });
  });

  it('keeps 0 verbatim as the unlimited cap', async () => {
    const update = await setup();
    await type('deck-settings-new-per-day', '0');
    await type('deck-settings-reviews-per-day', '0');
    await submit();
    expect(update).toHaveBeenCalledWith('d1', {
      new_per_day: 0,
      reviews_per_day: 0,
      preset_id: '3',
      autoplay: true,
      description_markdown: false,
    });
  });

  it('rejects an emptied cap before sending anything', async () => {
    const update = await setup();
    await type('deck-settings-new-per-day', '');
    await submit();
    expect(update).not.toHaveBeenCalled();
    // Local validation errors stay in the form and do not go through a toast.
    expect(
      document.body.querySelector('[data-testid="deck-settings-invalid"]')?.textContent
    ).toContain('请输入不小于 0 的整数');
    expect(toast.error).not.toHaveBeenCalled();
  });

  it('reports a forbidden save as an error toast', async () => {
    const update = await setup();
    update.mockRejectedValue(new ApiClientError('HTTP 403: forbidden', { status: 403, code: 'forbidden' }));
    await type('deck-settings-new-per-day', '15');
    await submit();
    expect(toast.error).toHaveBeenCalledWith(formatMessage('zh-CN', 'deck.settings.error.forbidden'));
    expect(toast.success).not.toHaveBeenCalled();
  });
});
