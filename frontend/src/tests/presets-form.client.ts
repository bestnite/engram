import { describe, it, expect, afterEach, vi } from 'vitest';
import { mount, unmount, flushSync } from 'svelte';
import PresetsView from '../lib/views/PresetsView.svelte';
import { apiClient } from '../lib/api';
import type { PresetsResponse, PresetRecord } from '../lib/api';
import { setLocale } from '../lib/i18n';

/**
 * 预设表单的「最大间隔」是字符串状态 + 数字输入框：绑定若把值强转成 number，
 * parseForm 的 trim 校验就抛异常，改完参数永远保存不了。
 */
describe('PresetsView max interval saving', () => {
  let instance: ReturnType<typeof mount> | null = null;
  let target: HTMLElement;

  const payload: PresetsResponse = {
    presets: [
      {
        id: '8',
        name: 'Evening',
        is_default: false,
        desired_retention: 0.9,
        learning_steps: '1m,10m',
        relearning_steps: '10m',
        maximum_interval_days: 36500,
        enable_fuzz: true,
        weights_optimized: false,
        weights_optimized_at: null,
        weights_review_count: null,
        weights_raw: null,
        job: null,
        gate: { reviews: 0, min: 500, shortfall: 500, eligible: false },
      } satisfies PresetRecord,
    ],
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
  async function type(testId: string, value: string): Promise<void> {
    const el = document.body.querySelector(`[data-testid="${testId}"]`) as HTMLInputElement | null;
    expect(el, testId).not.toBeNull();
    el!.value = value;
    el!.dispatchEvent(new Event('input', { bubbles: true }));
    await flush();
  }

  async function submit(): Promise<void> {
    const form = document.body.querySelector('[data-testid="presets-form"]') as HTMLFormElement | null;
    expect(form).not.toBeNull();
    form!.dispatchEvent(new Event('submit', { bubbles: true, cancelable: true }));
    await flush();
  }

  async function setup(): Promise<ReturnType<typeof vi.fn>> {
    setLocale('zh-CN');
    const update = vi.spyOn(apiClient, 'updatePreset').mockResolvedValue(payload);
    target = document.createElement('div');
    document.body.appendChild(target);
    instance = mount(PresetsView, {
      target,
      props: { initialLoading: false, initialData: payload },
    });
    await flush();
    const edit = document.body.querySelector('[data-testid="preset-8-edit"]') as HTMLElement | null;
    expect(edit).not.toBeNull();
    edit!.click();
    await flush();
    return update as unknown as ReturnType<typeof vi.fn>;
  }

  it('saves the typed maximum interval instead of throwing out of the submit handler', async () => {
    const update = await setup();
    await type('presets-form-max-interval', '999');
    await submit();
    expect(update).toHaveBeenCalledWith('8', {
      name: 'Evening',
      desired_retention: 0.9,
      learning_steps: '1m,10m',
      relearning_steps: '10m',
      maximum_interval_days: 999,
      enable_fuzz: true,
    });
  });

  it('rejects an emptied maximum interval before sending anything', async () => {
    const update = await setup();
    await type('presets-form-max-interval', '');
    await submit();
    expect(update).not.toHaveBeenCalled();
    // 提示必须是语言包文案，不是 key 本身：对话框曾经把 formError 原样渲染。
    const error = document.body.querySelector('[data-testid="presets-form-error"]');
    expect(error?.textContent).toContain('最大间隔必须是正整数天数。');
    expect(error?.textContent).not.toContain('presets.form.error');
  });
});
