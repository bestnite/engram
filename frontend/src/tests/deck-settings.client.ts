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
 * 每日上限的保存路径只有真实挂载才会走到：输入框写进的是字符串状态，若绑定把值强转成
 * number/null，parseCap 的 trim 校验就会抛异常，PATCH 请求根本发不出去。
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
    new_per_day: 10,
    reviews_per_day: 10,
    new_used: 2,
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

  /** 与浏览器同一条路径：先写 value 再派发 input，让绑定把值收进状态。 */
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

  async function setup(): Promise<ReturnType<typeof vi.fn>> {
    setLocale('zh-CN');
    routeStore.set(matchRoute('/decks/d1/settings'));
    vi.spyOn(apiClient, 'listPresets').mockResolvedValue({ presets: [] } as never);
    const update = vi
      .spyOn(apiClient, 'updateDeckSettings')
      .mockResolvedValue({ ...settings, new_per_day: 15, new_left: 13 });
    target = document.createElement('div');
    document.body.appendChild(target);
    instance = mount(DeckSettingsView, {
      target,
      props: { initialLoading: false, initialSettings: settings },
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
    });
    expect(toast.success).toHaveBeenCalledWith(formatMessage('zh-CN', 'deck.settings.saved'));
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
    });
  });

  it('rejects an emptied cap before sending anything', async () => {
    const update = await setup();
    await type('deck-settings-new-per-day', '');
    await submit();
    expect(update).not.toHaveBeenCalled();
    // 本地校验错误留在表单里，不走 toast。
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
