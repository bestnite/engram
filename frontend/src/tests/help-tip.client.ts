import { describe, it, expect, afterEach } from 'vitest';
import { mount, unmount, flushSync } from 'svelte';
import HelpTip from '../lib/components/ui/HelpTip.svelte';
import { setLocale } from '../lib/i18n';

/**
 * HelpTip's interaction contract: tap (touch and mouse share one path) opens, tapping
 * outside closes; Enter opens, Esc closes. A hover-only tip cannot be opened on a phone,
 * so hover is not tested here — only tap and keyboard.
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

    // Once open, focus is inside the popover; Esc bubbles from the focused element up to document.
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

    // The library decides by coordinates whether the click really landed outside; in happy-dom the popover rect is 0×0, so a tap at (200, 200) counts as outside.
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
