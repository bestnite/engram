import { describe, it, expect, afterEach, vi } from 'vitest';
import { mount, unmount, flushSync } from 'svelte';
import SettingsView from '../lib/views/SettingsView.svelte';
import { apiClient } from '../lib/api';
import type { UserProfile } from '../lib/api';
import { setLocale } from '../lib/i18n';

/**
 * 「提前学习窗口」是字符串状态 + 数字输入框：绑定若把值强转成 number，
 * validateLearnAheadMinutes 的 trim 校验就抛异常，整份个人资料都保存不了。
 */
describe('SettingsView learn-ahead saving', () => {
  let instance: ReturnType<typeof mount> | null = null;
  let target: HTMLElement;

  const profile: UserProfile = {
    display_name: 'nite',
    locale: 'zh-CN',
    timezone: 'UTC',
    day_cutoff_hour: 4,
    learn_ahead_minutes: 20,
  };

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

  /** 与浏览器同一条路径：先写 value 再派发 input，让绑定把值收进状态。 */
  async function type(value: string): Promise<void> {
    const el = document.body.querySelector('[data-testid="settings-learn-ahead"]') as HTMLInputElement | null;
    expect(el).not.toBeNull();
    el!.value = value;
    el!.dispatchEvent(new Event('input', { bubbles: true }));
    await flush();
  }

  async function submit(): Promise<void> {
    const button = document.body.querySelector('[data-testid="settings-submit"]');
    const form = button?.closest('form') as HTMLFormElement | null;
    expect(form).not.toBeNull();
    form!.dispatchEvent(new Event('submit', { bubbles: true, cancelable: true }));
    await flush();
  }

  async function setup(): Promise<ReturnType<typeof vi.fn>> {
    setLocale('zh-CN');
    vi.spyOn(apiClient, 'getProfile').mockResolvedValue(profile);
    vi.spyOn(apiClient, 'getShareInvites').mockResolvedValue({ policy: 'anyone', allow_list: [] } as never);
    const update = vi.spyOn(apiClient, 'updateProfile').mockResolvedValue(profile);
    target = document.createElement('div');
    document.body.appendChild(target);
    instance = mount(SettingsView, { target, props: { initialLoading: false } });
    await flush();
    return update as unknown as ReturnType<typeof vi.fn>;
  }

  it('saves the typed window instead of throwing out of the submit handler', async () => {
    const update = await setup();
    await type('45');
    await submit();
    expect(update).toHaveBeenCalledWith({
      display_name: 'nite',
      locale: 'zh-CN',
      timezone: 'UTC',
      day_cutoff_hour: 4,
      learn_ahead_minutes: 45,
    });
    expect(document.body.querySelector('[data-testid="settings-saved-notice"]')).not.toBeNull();
  });

  it('sends nothing when the typed window is not a whole number', async () => {
    const update = await setup();
    await type('1.5');
    await submit();
    expect(update).not.toHaveBeenCalled();
    expect(
      document.body.querySelector('[data-testid="settings-error-learn-ahead"]')?.textContent
    ).toContain('请输入 0 到 1440 之间的整数');
  });
});
