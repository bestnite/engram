import { describe, it, expect, afterEach } from 'vitest';
import { mount, unmount, flushSync, createRawSnippet } from 'svelte';
import { Layers } from '@lucide/svelte';
import EmptyState from '../lib/components/ui/EmptyState.svelte';

/**
 * EmptyState: the icon names what is missing but is decorative, so the text alone is what a
 * screen reader hears; the optional action renders below the text, and a caller's class
 * overrides the default padding instead of stacking on top of it.
 */
describe('EmptyState', () => {
  let instance: ReturnType<typeof mount> | null = null;

  afterEach(() => {
    if (instance) unmount(instance);
    instance = null;
    document.body.innerHTML = '';
  });

  function render(props: Record<string, unknown>): HTMLElement {
    const target = document.createElement('div');
    document.body.appendChild(target);
    instance = mount(EmptyState, { target, props: { icon: Layers, text: 'No decks yet.', testId: 'empty', ...props } as never });
    flushSync();
    return target.querySelector('[data-testid="empty"]') as HTMLElement;
  }

  it('shows the text with a decorative icon and no action', () => {
    const root = render({});
    expect(root.textContent?.trim()).toBe('No decks yet.');
    const icon = root.querySelector('svg.lucide-layers');
    expect(icon).not.toBeNull();
    expect(icon?.getAttribute('aria-hidden')).toBe('true');
    expect(root.querySelector('button')).toBeNull();
    expect(root.className).toContain('py-16');
  });

  it('renders the action below the text', () => {
    const children = createRawSnippet(() => ({ render: () => '<button type="button">Create</button>' }));
    const root = render({ children });
    const text = root.querySelector('p');
    const button = root.querySelector('button');
    expect(button?.textContent).toBe('Create');
    expect(text && button && text.compareDocumentPosition(button) & Node.DOCUMENT_POSITION_FOLLOWING).toBeTruthy();
  });

  it('lets the caller replace the default padding', () => {
    const root = render({ class: 'py-10' });
    expect(root.className).toContain('py-10');
    expect(root.className).not.toContain('py-16');
  });
});
