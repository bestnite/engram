import { describe, it, expect, afterEach, vi } from 'vitest';
import { mount, unmount, flushSync } from 'svelte';
import InlineEdit from '../lib/components/InlineEdit.svelte';

/**
 * In-place editing's interaction contract: a click enters edit mode; Enter saves, Esc
 * cancels; when saving fails (onSave returns false) it stays in edit mode and the input is
 * not lost.
 */
describe('InlineEdit', () => {
  let instance: ReturnType<typeof mount> | null = null;
  let target: HTMLElement;

  afterEach(() => {
    if (instance) unmount(instance);
    instance = null;
    document.body.innerHTML = '';
  });

  async function flush(): Promise<void> {
    for (let i = 0; i < 10; i++) await Promise.resolve();
    flushSync();
  }

  function setup(onSave: (v: string) => Promise<boolean>, editable = true) {
    target = document.createElement('div');
    document.body.appendChild(target);
    instance = mount(InlineEdit, { target, props: { value: 'Biology', onSave, label: 'Name', testId: 'deck-name', editable } });
    flushSync();
  }

  async function startEditing(): Promise<HTMLInputElement> {
    (target.querySelector('[data-testid="deck-name-edit"]') as HTMLButtonElement).click();
    await flush();
    return target.querySelector('[data-testid="deck-name-input"]') as HTMLInputElement;
  }

  function type(input: HTMLInputElement, value: string): void {
    input.value = value;
    input.dispatchEvent(new Event('input', { bubbles: true }));
    flushSync();
  }

  function press(input: HTMLInputElement, key: string): void {
    input.dispatchEvent(new KeyboardEvent('keydown', { key, bubbles: true, cancelable: true }));
  }

  it('saves on Enter and returns to display mode', async () => {
    const onSave = vi.fn(async () => true);
    setup(onSave);
    const input = await startEditing();
    type(input, 'Chemistry');
    press(input, 'Enter');
    await flush();
    expect(onSave).toHaveBeenCalledWith('Chemistry');
    expect(target.querySelector('[data-testid="deck-name-input"]')).toBeNull();
  });

  it('cancels on Escape without saving', async () => {
    const onSave = vi.fn(async () => true);
    setup(onSave);
    const input = await startEditing();
    type(input, 'Chemistry');
    press(input, 'Escape');
    await flush();
    expect(onSave).not.toHaveBeenCalled();
    expect(target.querySelector('[data-testid="deck-name"]')?.textContent).toBe('Biology');
  });

  it('stays in edit mode with the draft kept when saving fails', async () => {
    setup(async () => false);
    const input = await startEditing();
    type(input, '');
    press(input, 'Enter');
    await flush();
    const still = target.querySelector('[data-testid="deck-name-input"]') as HTMLInputElement | null;
    expect(still).not.toBeNull();
    expect(still?.value).toBe('');
  });

  it('renders no edit control when not editable', () => {
    setup(async () => true, false);
    expect(target.querySelector('[data-testid="deck-name-edit"]')).toBeNull();
    expect(target.textContent).toContain('Biology');
  });
});
