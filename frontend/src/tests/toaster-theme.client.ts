import { describe, it, expect, afterEach, beforeEach } from 'vitest';
import { mount, unmount, flushSync } from 'svelte';
import Toaster from '../lib/components/ui/Toaster.svelte';
import { toast } from '../lib/components/ui/toast';

/**
 * sonner keeps its own light/dark palette for the parts the app's CSS variables do not cover — most visibly the
 * close button: with sonner's default `light` theme, dark mode drew a near-black icon on the dark toast, so the
 * button read as a black blob until hover changed its background. The Toaster must therefore mirror the `.dark`
 * class on <html> into sonner's `theme` prop, on mount and whenever the class changes.
 */
describe('Toaster follows the app theme', () => {
  let instance: ReturnType<typeof mount> | null = null;
  let target: HTMLElement;

  beforeEach(() => {
    document.documentElement.classList.remove('dark');
    target = document.createElement('div');
    document.body.appendChild(target);
  });

  afterEach(() => {
    if (instance) unmount(instance);
    instance = null;
    document.body.innerHTML = '';
    document.documentElement.classList.remove('dark');
  });

  async function flush(): Promise<void> {
    for (let i = 0; i < 20; i++) await Promise.resolve();
    flushSync();
  }

  /** sonner renders its list (the element carrying data-sonner-theme) only while at least one toast is visible. */
  async function showToastAndTheme(): Promise<string | null> {
    toast('probe');
    await flush();
    const el = document.body.querySelector('[data-sonner-toaster]');
    expect(el).not.toBeNull();
    return el!.getAttribute('data-sonner-theme');
  }

  it('renders sonner in dark mode when the page already carries the dark class', async () => {
    document.documentElement.classList.add('dark');
    instance = mount(Toaster, { target });
    await flush();
    expect(await showToastAndTheme()).toBe('dark');
  });

  it('switches sonner along when the theme class changes after mount', async () => {
    instance = mount(Toaster, { target });
    await flush();
    expect(await showToastAndTheme()).toBe('light');

    document.documentElement.classList.add('dark');
    await flush();
    expect(await showToastAndTheme()).toBe('dark');

    document.documentElement.classList.remove('dark');
    await flush();
    expect(await showToastAndTheme()).toBe('light');
  });
});
