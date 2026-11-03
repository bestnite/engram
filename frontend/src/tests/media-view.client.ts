import { describe, it, expect, afterEach, vi } from 'vitest';
import { mount, unmount, flushSync } from 'svelte';
import MediaView from '../lib/views/MediaView.svelte';
import { apiClient } from '../lib/api';
import type { MediaItem, MediaListResponse } from '../lib/api';
import { setLocale } from '../lib/i18n';

/**
 * The global media browser page: grid rendering (thumbnail, names, mime/size, created time, state badge and cleanup
 * copy), the search filter going through the listing method, "load more" paging with the cursor, and the
 * empty/error/retry states.
 */
describe('media browser view', () => {
  let instance: ReturnType<typeof mount> | null = null;
  let target: HTMLElement;

  afterEach(() => {
    if (instance) unmount(instance);
    instance = null;
    document.body.innerHTML = '';
    vi.restoreAllMocks();
  });

  function media(sha: string, overrides: Partial<MediaItem> = {}): MediaItem {
    return {
      sha256: sha,
      mime: 'image/png',
      bytes: 1536,
      url: `/media/${sha}`,
      width: null,
      height: null,
      created_at: '2026-10-01T00:00:00Z',
      names: [],
      decks: [],
      state: 'referenced',
      cleanup_at: null,
      ...overrides,
    };
  }

  function page(items: MediaItem[], nextCursor = ''): MediaListResponse {
    return { items, next_cursor: nextCursor };
  }

  async function flush(): Promise<void> {
    for (let i = 0; i < 20; i++) await Promise.resolve();
    flushSync();
  }

  async function mountView(): Promise<void> {
    setLocale('zh-CN');
    target = document.createElement('div');
    document.body.appendChild(target);
    vi.spyOn(apiClient, 'getDecks').mockResolvedValue({ decks: [] } as never);
    instance = mount(MediaView, { target });
    await flush();
  }

  function query(testId: string): HTMLElement | null {
    return target.querySelector(`[data-testid="${testId}"]`);
  }

  it('renders items with thumbnail, names, mime, size, state badge and cleanup copy', async () => {
    vi.spyOn(apiClient, 'listMedia').mockResolvedValueOnce(
      page([
        media('a'.repeat(64), {
          names: ['photo.png'],
          decks: [{ id: 'deck-1', name: 'Alpha' }],
          state: 'pending',
          cleanup_at: '2026-12-01T00:00:00Z',
        }),
        media('b'.repeat(64), {
          mime: 'audio/mpeg',
          bytes: 2048,
          names: [],
          state: 'referenced',
          cleanup_at: null,
        }),
      ])
    );
    await mountView();

    expect(query('media-page')).not.toBeNull();
    expect(target.querySelectorAll('[data-testid="media-item"]')).toHaveLength(2);
    const text = target.textContent ?? '';
    expect(text).toContain('photo.png');
    expect(text).toContain('Alpha');
    expect(text).toContain('待清理');
    expect(text).toContain('下次清扫将在');
    expect(text).toContain('已引用');
    expect(text).toContain('未计划清理');
    expect(text).toContain('audio/mpeg');
    expect(text).toContain('2 KiB');

    // Only the image item renders the thumbnail, through the existing media URL.
    const thumbnails = target.querySelectorAll('[data-testid="media-thumbnail"]');
    expect(thumbnails).toHaveLength(1);
    expect(thumbnails[0]?.getAttribute('src')).toBe(`/media/${'a'.repeat(64)}`);

    // The first load sends no filter at all.
    expect(apiClient.listMedia).toHaveBeenCalledWith({ limit: 24 });
  });

  it('shows the elapsed-grace copy for a pending item whose cleanup time has passed', async () => {
    vi.spyOn(apiClient, 'listMedia').mockResolvedValueOnce(
      page([media('c'.repeat(64), { names: ['old.png'], state: 'pending', cleanup_at: '2020-01-01T00:00:00Z' })])
    );
    await mountView();
    expect(target.textContent ?? '').toContain('宽限期已过');
  });

  it('pages with the cursor returned by the first response', async () => {
    const listMedia = vi
      .spyOn(apiClient, 'listMedia')
      .mockResolvedValueOnce(page([media('d'.repeat(64), { names: ['first.png'] })], 'cursor-1'))
      .mockResolvedValueOnce(page([media('e'.repeat(64), { names: ['second.png'] })]));
    await mountView();

    expect(target.querySelectorAll('[data-testid="media-item"]')).toHaveLength(1);
    const loadMore = query('media-load-more');
    expect(loadMore).not.toBeNull();
    loadMore!.click();
    await flush();

    expect(listMedia).toHaveBeenCalledTimes(2);
    expect(listMedia).toHaveBeenLastCalledWith({ limit: 24, cursor: 'cursor-1' });
    expect(target.querySelectorAll('[data-testid="media-item"]')).toHaveLength(2);
    expect(target.textContent ?? '').toContain('second.png');
    // The second page was the end, so the button is gone.
    expect(query('media-load-more')).toBeNull();
  });

  it('searches after the debounce and sends the typed text as q', async () => {
    const listMedia = vi
      .spyOn(apiClient, 'listMedia')
      .mockResolvedValue(page([media('f'.repeat(64), { names: ['photo.png'] })]));
    await mountView();

    const input = query('media-search') as HTMLInputElement;
    input.value = 'photo';
    input.dispatchEvent(new Event('input', { bubbles: true }));
    await new Promise((resolve) => setTimeout(resolve, 350));
    await flush();

    expect(listMedia).toHaveBeenCalledTimes(2);
    expect(listMedia).toHaveBeenLastCalledWith({ limit: 24, q: 'photo' });
  });

  it('shows the empty state and the no-match empty state', async () => {
    vi.spyOn(apiClient, 'listMedia').mockResolvedValue(page([]));
    await mountView();
    expect(query('media-empty')).not.toBeNull();
    expect(target.textContent ?? '').toContain('还没有媒体');

    const input = query('media-search') as HTMLInputElement;
    input.value = 'nothing';
    input.dispatchEvent(new Event('input', { bubbles: true }));
    await new Promise((resolve) => setTimeout(resolve, 350));
    await flush();
    expect(target.textContent ?? '').toContain('没有符合这些筛选条件的媒体');
  });

  it('shows the error state and retries the same listing', async () => {
    const listMedia = vi
      .spyOn(apiClient, 'listMedia')
      .mockRejectedValueOnce(new Error('network down'))
      .mockResolvedValueOnce(page([media('a'.repeat(64), { names: ['retried.png'] })]));
    await mountView();

    expect(query('media-failed')).not.toBeNull();
    expect(target.textContent ?? '').toContain('无法加载媒体库');
    query('media-retry')!.click();
    await flush();

    expect(listMedia).toHaveBeenCalledTimes(2);
    expect(target.querySelectorAll('[data-testid="media-item"]')).toHaveLength(1);
    expect(target.textContent ?? '').toContain('retried.png');
  });
});
