import { describe, it, expect, afterEach } from 'vitest';
import { mount, unmount, flushSync } from 'svelte';
import HelpTip from '../lib/components/ui/HelpTip.svelte';
import { setLocale } from '../lib/i18n';

/**
 * HelpTip 的交互契约：点按（触屏与鼠标同一路径）打开、点外部关闭；键盘 Enter 打开、Esc 关闭。
 * 只靠悬停的提示在手机上打不开，所以这里不测悬停，只测点按与键盘。
 */
describe('HelpTip (mounted)', () => {
  let instance: ReturnType<typeof mount> | null = null;

  afterEach(() => {
    if (instance) unmount(instance);
    instance = null;
    document.body.innerHTML = '';
  });

  async function flush(): Promise<void> {
    for (let i = 0; i < 20; i++) await Promise.resolve();
    flushSync();
    await new Promise((resolve) => setTimeout(resolve, 0));
    flushSync();
  }

  function setup(): HTMLButtonElement {
    setLocale('zh-CN');
    const target = document.createElement('div');
    document.body.appendChild(target);
    instance = mount(HelpTip, {
      target,
      props: { label: '留存率', text: '到期复习时答对的比例。', testId: 'tip' },
    });
    flushSync();
    return document.body.querySelector('[data-testid="tip"]') as HTMLButtonElement;
  }

  function text(): HTMLElement | null {
    return document.body.querySelector('[data-testid="tip-text"]');
  }

  it('opens on click and shows the explanation', async () => {
    const button = setup();
    expect(button.getAttribute('aria-label')).toBe('留存率是什么意思？');
    expect(text()).toBeNull();

    button.click();
    await flush();

    expect(button.getAttribute('aria-expanded')).toBe('true');
    expect(text()?.textContent).toBe('到期复习时答对的比例。');
  });

  it('closes on Escape', async () => {
    const button = setup();
    button.click();
    await flush();
    expect(text()).not.toBeNull();
    expect(document.activeElement).not.toBe(document.body);

    // 打开后焦点在浮层里，Esc 从焦点处冒泡到 document。
    (document.activeElement ?? document.body).dispatchEvent(new KeyboardEvent('keydown', { key: 'Escape', bubbles: true }));
    await flush();

    expect(button.getAttribute('aria-expanded')).toBe('false');
  });

  it('opens with the Enter key', async () => {
    const button = setup();
    button.focus();
    button.dispatchEvent(new KeyboardEvent('keydown', { key: 'Enter', bubbles: true }));
    await flush();

    expect(button.getAttribute('aria-expanded')).toBe('true');
    expect(text()?.textContent).toBe('到期复习时答对的比例。');
  });

  it('closes when the user taps outside', async () => {
    const button = setup();
    button.click();
    await flush();
    expect(text()).not.toBeNull();

    // 库按坐标判断是否真在浮层外；happy-dom 里浮层的矩形是 0×0，点按落在 (200, 200) 即在外部。
    const outside = document.createElement('div');
    document.body.appendChild(outside);
    const at = { bubbles: true, button: 0, pointerType: 'touch', clientX: 200, clientY: 200 };
    outside.dispatchEvent(new PointerEvent('pointerdown', at));
    outside.dispatchEvent(new PointerEvent('pointerup', at));
    outside.dispatchEvent(new MouseEvent('click', { bubbles: true, button: 0, clientX: 200, clientY: 200 }));
    await flush();

    expect(button.getAttribute('aria-expanded')).toBe('false');
  });
});
