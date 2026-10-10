import { describe, it, expect, afterEach, vi } from 'vitest';
import { mount, unmount, flushSync, createRawSnippet } from 'svelte';
import Button from '../lib/components/ui/Button.svelte';

/**
 * 请求进行中的唯一表示：按钮禁用并转圈，文字不换、也不从读屏里消失；
 * 进行中不能再次触发同一个请求。
 */
describe('Button loading', () => {
  let instance: ReturnType<typeof mount> | null = null;
  let target: HTMLElement;

  afterEach(() => {
    if (instance) unmount(instance);
    instance = null;
    document.body.innerHTML = '';
  });

  const label = createRawSnippet(() => ({ render: () => '<span>Save</span>' }));

  function setup(props: { loading?: boolean; disabled?: boolean; onclick?: () => void }): HTMLButtonElement {
    target = document.createElement('div');
    document.body.appendChild(target);
    instance = mount(Button, { target, props: { testId: 'btn', children: label, ...props } });
    flushSync();
    return target.querySelector('[data-testid="btn"]') as HTMLButtonElement;
  }

  it('disables the button, marks it busy and keeps the label text', () => {
    const onclick = vi.fn();
    const button = setup({ loading: true, onclick });
    expect(button.disabled).toBe(true);
    expect(button.getAttribute('aria-busy')).toBe('true');
    expect(button.textContent?.trim()).toBe('Save');
    expect(button.querySelector('svg.animate-spin')).not.toBeNull();
    button.click();
    expect(onclick).not.toHaveBeenCalled();
  });

  it('renders no spinner and no busy state when idle', () => {
    const onclick = vi.fn();
    const button = setup({ onclick });
    expect(button.disabled).toBe(false);
    expect(button.hasAttribute('aria-busy')).toBe(false);
    expect(button.querySelector('svg.animate-spin')).toBeNull();
    button.click();
    expect(onclick).toHaveBeenCalledTimes(1);
  });

  it('stays disabled without a spinner when only disabled', () => {
    const button = setup({ disabled: true });
    expect(button.disabled).toBe(true);
    expect(button.hasAttribute('aria-busy')).toBe(false);
    expect(button.querySelector('svg.animate-spin')).toBeNull();
  });
});
