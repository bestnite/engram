import { describe, it, expect, vi, beforeEach, afterEach, type Mock } from 'vitest';
import { mount, unmount, flushSync } from 'svelte';
import MediaPicker from '../lib/components/MediaPicker.svelte';
import Harness from './media-picker-harness.svelte';
import { apiClient } from '../lib/api';
import type { MediaItem } from '../lib/api';
import { setLocale } from '../lib/i18n';

/**
 * The media picker dialog mounted on its own: the list request its controls build, paging, the empty/error states, and
 * the hand-off of a chosen item. The client calls themselves are covered by media-picker.test.ts; the editor integration
 * is covered by rich-text-field.client.ts and note-editor.client.ts.
 */

/** One library item; sha 3 is the last six of a valid digest, so ids stay readable. */
function item(n: number, overrides: Partial<MediaItem> = {}): MediaItem {
  const sha = n.toString(16).padStart(2, '0').repeat(32);
  return {
    sha256: sha,
    mime: 'image/png',
    bytes: 1024 * n,
    url: `/media/${sha}`,
    created_at: '2026-11-01T00:00:00Z',
    names: [],
    decks: [],
    state: 'referenced',
    cleanup_at: null,
    ...overrides,
  };
}

let target: HTMLElement;
let instance: ReturnType<typeof mount> | null = null;
let onchoose: Mock<(item: MediaItem) => Promise<void>>;
let listMedia: ReturnType<typeof vi.spyOn>;

/** Flush microtasks and apply Svelte's pending state synchronously so assertions see the real DOM. */
async function flush(times = 24): Promise<void> {
  for (let i = 0; i < times; i += 1) await Promise.resolve();
  flushSync();
}

function mountPicker(props: Record<string, unknown> = {}): void {
  target = document.createElement('div');
  document.body.appendChild(target);
  instance = mount(MediaPicker, {
    target,
    props: {
      open: true,
      deckId: '',
      onchoose,
      testId: 'picker',
      ...props,
    },
  });
}

function dialog(): HTMLElement | null {
  return document.body.querySelector('[data-testid="picker"]');
}

function element<T extends HTMLElement>(testid: string): T | null {
  return document.body.querySelector(`[data-testid="${testid}"]`) as T | null;
}

/** Opens a bits-ui Select and commits one option by its visible label, the way a pointer click does. */
async function chooseOption(testId: string, label: string): Promise<void> {
  const trigger = element<HTMLElement>(testId);
  if (!trigger) throw new Error(`missing select ${testId}`);
  trigger.focus();
  trigger.dispatchEvent(new KeyboardEvent('keydown', { key: 'Enter', bubbles: true }));
  await flush();
  const option = Array.from(document.body.querySelectorAll('[role="option"]')).find(
    (candidate) => candidate.textContent?.trim() === label
  ) as HTMLElement | undefined;
  if (!option) throw new Error(`missing option ${label}`);
  option.dispatchEvent(new PointerEvent('pointerdown', { bubbles: true, cancelable: true }));
  option.dispatchEvent(new PointerEvent('pointerup', { bubbles: true, cancelable: true }));
  await flush();
}

beforeEach(() => {
  setLocale('en');
  document.body.innerHTML = '';
  onchoose = vi.fn(async (_item: MediaItem) => {});
  listMedia = vi.spyOn(apiClient, 'listMediaForPicker');
});

afterEach(() => {
  if (instance) unmount(instance);
  instance = null;
  vi.restoreAllMocks();
  document.body.innerHTML = '';
});

describe('media picker list and filters', () => {
  it('loads the first page on open and renders image thumbnails from the media route', async () => {
    const image = item(1);
    listMedia.mockResolvedValueOnce({ items: [image], next_cursor: '' });
    mountPicker();
    await flush();

    expect(listMedia).toHaveBeenCalledWith('', {});
    expect(element('picker-item-' + image.sha256)).toBeTruthy();
    const thumb = element<HTMLImageElement>('picker-thumb-' + image.sha256);
    expect(thumb?.getAttribute('src')).toBe(image.url);
    // The dialog's title comes from the shared library copy rather than a picker-specific duplicate.
    expect(dialog()?.textContent).toContain('Media library');
  });

  it('sends the search box through the debounced q filter', async () => {
    listMedia.mockResolvedValue({ items: [], next_cursor: '' });
    mountPicker();
    await flush();
    expect(listMedia).toHaveBeenLastCalledWith('', {});

    const search = element<HTMLInputElement>('picker-search')!;
    search.value = 'map';
    search.dispatchEvent(new Event('input', { bubbles: true }));
    // The request fires only after the debounce window, not on each keystroke.
    expect(listMedia).toHaveBeenCalledTimes(1);
    await vi.waitFor(() => expect(listMedia).toHaveBeenLastCalledWith('', { q: 'map' }));
  });

  it('sends the selected class and deck scope as filters', async () => {
    listMedia.mockResolvedValue({ items: [], next_cursor: '' });
    mountPicker({ deckId: 'deck-1' });
    await flush();

    await chooseOption('picker-class', 'Audio');
    expect(listMedia).toHaveBeenLastCalledWith('', { class: 'audio' });

    await chooseOption('picker-deck-scope', 'This deck');
    expect(listMedia).toHaveBeenLastCalledWith('', { class: 'audio', deck: 'deck-1' });

    await chooseOption('picker-class', 'All types');
    await chooseOption('picker-deck-scope', 'All decks');
    expect(listMedia).toHaveBeenLastCalledWith('', {});
  });

  it('hides the deck scope control when no deck is being edited', async () => {
    listMedia.mockResolvedValue({ items: [], next_cursor: '' });
    mountPicker();
    await flush();
    expect(element('picker-deck-scope')).toBeNull();
  });
});

describe('media picker paging and states', () => {
  it('appends the next page and hides the button at the end', async () => {
    const first = item(1);
    const second = item(2);
    listMedia.mockResolvedValueOnce({ items: [first], next_cursor: 'cursor-1' });
    listMedia.mockResolvedValueOnce({ items: [second], next_cursor: '' });
    mountPicker();
    await flush();

    const next = element<HTMLButtonElement>('picker-next')!;
    expect(next).toBeTruthy();
    next.click();
    await flush();

    expect(listMedia).toHaveBeenLastCalledWith('cursor-1', {});
    expect(element('picker-item-' + first.sha256)).toBeTruthy();
    expect(element('picker-item-' + second.sha256)).toBeTruthy();
    expect(element('picker-next')).toBeNull();
  });

  it('shows the empty state, and the filtered copy once a filter is active', async () => {
    listMedia.mockResolvedValue({ items: [], next_cursor: '' });
    mountPicker();
    await flush();
    expect(element('picker-empty')?.textContent).toContain('No media yet.');

    await chooseOption('picker-class', 'Video');
    expect(element('picker-empty')?.textContent).toContain('No media matches these filters.');
  });

  it('offers a retry when the first page fails', async () => {
    const image = item(1);
    listMedia.mockRejectedValueOnce(new Error('offline'));
    listMedia.mockResolvedValueOnce({ items: [image], next_cursor: '' });
    mountPicker();
    await flush();

    expect(element('picker-error')?.textContent).toContain('Could not load the media library.');
    element<HTMLButtonElement>('picker-retry')!.click();
    await flush();

    expect(listMedia).toHaveBeenCalledTimes(2);
    expect(element('picker-item-' + image.sha256)).toBeTruthy();
    expect(element('picker-error')).toBeNull();
  });

  it('keeps the loaded items when a later page fails and reports the failure', async () => {
    const first = item(1);
    listMedia.mockResolvedValueOnce({ items: [first], next_cursor: 'cursor-1' });
    listMedia.mockRejectedValueOnce(new Error('offline'));
    mountPicker();
    await flush();

    element<HTMLButtonElement>('picker-next')!.click();
    await flush();

    expect(element('picker-item-' + first.sha256)).toBeTruthy();
    expect(element('picker-more-error')?.textContent).toContain('Could not load more media.');
  });
});

describe('media picker selection', () => {
  it('hands the chosen item to the caller', async () => {
    const image = item(2);
    listMedia.mockResolvedValueOnce({ items: [image], next_cursor: '' });
    mountPicker();
    await flush();

    element<HTMLButtonElement>('picker-item-' + image.sha256)!.click();
    await flush();

    expect(onchoose).toHaveBeenCalledWith(image);
  });

  it('blocks a second choice while the first is in flight', async () => {
    let finish!: () => void;
    onchoose.mockImplementation(() => new Promise<void>((resolve) => (finish = resolve)));
    const image = item(2);
    listMedia.mockResolvedValue({ items: [image], next_cursor: '' });
    mountPicker();
    await flush();

    element<HTMLButtonElement>('picker-item-' + image.sha256)!.click();
    await flush();
    element<HTMLButtonElement>('picker-item-' + image.sha256)!.click();
    await flush();

    expect(onchoose).toHaveBeenCalledTimes(1);
    finish();
    await flush();
  });

  it('closes through its close button', async () => {
    listMedia.mockResolvedValueOnce({ items: [], next_cursor: '' });
    // The open state belongs to the parent, so observability needs a parent that binds it.
    target = document.createElement('div');
    document.body.appendChild(target);
    instance = mount(Harness, { target });
    await flush();
    expect(dialog()).toBeTruthy();

    element<HTMLButtonElement>('picker-close')!.click();
    await flush();
    expect(element<HTMLElement>('harness-open')?.textContent).toBe('closed');
  });
});
