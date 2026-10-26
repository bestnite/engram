import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest';
import { mount, unmount, flushSync } from 'svelte';
import ShortcodesHelpView from '../lib/views/ShortcodesHelpView.svelte';
import { setLocale } from '../lib/i18n';
import { zhCN } from '../lib/i18n/locales/zh-CN';
import { toast } from '../lib/components/ui/toast';

// The result of a copy is reported only through a toast, so the stand-in records the calls.
vi.mock('../lib/components/ui/toast', () => ({ toast: { success: vi.fn(), error: vi.fn(), warning: vi.fn() } }));

const TTS_EXAMPLE = '{{tts:こんにちは|lang=ja-JP|rate=0.9|hide}}';

function stubClipboard(writeText: ((text: string) => Promise<void>) | undefined): void {
  Object.defineProperty(navigator, 'clipboard', { value: writeText ? { writeText } : undefined, configurable: true });
}

describe('ShortcodesHelpView copy buttons', () => {
  let instance: ReturnType<typeof mount> | null = null;
  let target: HTMLElement;

  beforeEach(() => {
    setLocale('zh-CN');
    target = document.createElement('div');
    document.body.appendChild(target);
    instance = mount(ShortcodesHelpView, { target });
    flushSync();
  });

  afterEach(() => {
    if (instance) unmount(instance);
    instance = null;
    document.body.innerHTML = '';
    stubClipboard(undefined);
    vi.clearAllMocks();
  });

  async function clickCopy(name: string): Promise<void> {
    (target.querySelector(`[data-testid="shortcode-copy-${name}"]`) as HTMLButtonElement).click();
    // The handler awaits the clipboard promise; drain the microtask queue so the toast call has happened.
    for (let i = 0; i < 10; i++) await Promise.resolve();
    flushSync();
  }

  it('copies the example verbatim and confirms with a toast', async () => {
    const writeText = vi.fn(async () => {});
    stubClipboard(writeText);

    await clickCopy('tts');

    expect(writeText).toHaveBeenCalledWith(TTS_EXAMPLE);
    expect(toast.success).toHaveBeenCalledWith(zhCN['help.shortcode.copied']);
    expect(toast.error).not.toHaveBeenCalled();
  });

  it('reports a failed copy instead of pretending it worked', async () => {
    stubClipboard(async () => {
      throw new Error('denied');
    });

    await clickCopy('audio');

    expect(toast.error).toHaveBeenCalledWith(zhCN['help.shortcode.copy_failed']);
    expect(toast.success).not.toHaveBeenCalled();
  });

  it('does nothing silently when the clipboard API is absent', async () => {
    stubClipboard(undefined);

    await clickCopy('ruby');

    expect(toast.error).toHaveBeenCalledWith(zhCN['help.shortcode.copy_failed']);
    expect(toast.success).not.toHaveBeenCalled();
  });
});
