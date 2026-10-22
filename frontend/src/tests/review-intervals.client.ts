import { describe, it, expect, afterEach, vi } from 'vitest';
import { mount, unmount, flushSync } from 'svelte';
import ReviewView from '../lib/views/ReviewView.svelte';
import type { ApiClient, DueCard, RatingInterval } from '../lib/api';
import { setLocale } from '../lib/i18n';
import { clearCardTypes, seedCardTypes } from './card-type-fixture';

/**
 * After flipping a self-rated card, each rating button shows the next interval for that
 * rating (from the render response), with a "how to choose" help popover beside the rating
 * row; when render gives no intervals the buttons show only the rating names.
 */
describe('ReviewView rating intervals and guidance', () => {
  let instance: ReturnType<typeof mount> | null = null;

  afterEach(() => {
    if (instance) unmount(instance);
    instance = null;
    document.body.innerHTML = '';
    clearCardTypes();
    vi.restoreAllMocks();
  });

  async function flush(): Promise<void> {
    for (let i = 0; i < 20; i++) await Promise.resolve();
    flushSync();
  }

  const card: DueCard = {
    card_id: 'c1',
    note_id: 'n1',
    deck_id: 'd1',
    state: 'new',
    due_at: '',
    retrievability: 0,
    kind: 'basic',
    fields: { front: 'q', back: 'a' },
    tags: [],
    template: 'forward',
    version: 0,
  };

  async function setup(intervals: RatingInterval[] | undefined) {
    setLocale('zh-CN');
    seedCardTypes();
    const client = {
      getDueCards: vi.fn().mockResolvedValue({ cards: [card] }),
      renderReviewCard: vi.fn().mockResolvedValue({ card_id: 'c1', front_html: '<p>q</p>', back_html: '<p>a</p>', blanks: null, edit_href: '', intervals }),
      getCardTypes: vi.fn(),
    } as unknown as ApiClient;
    const target = document.createElement('div');
    document.body.appendChild(target);
    instance = mount(ReviewView, { target, props: { client } });
    await flush();
    (target.querySelector('[data-testid="review-reveal"]') as HTMLElement).click();
    await flush();
    return target;
  }

  it('shows each rating\'s next interval under its label', async () => {
    const target = await setup([
      { rating: 1, seconds: 60 },
      { rating: 2, seconds: 360 },
      { rating: 3, seconds: 600 },
      { rating: 4, seconds: 8 * 86400 },
    ]);
    const labels = Array.from(target.querySelectorAll('[data-rating]')).map((el) => el.textContent?.replace(/\s+/g, ' ').trim());
    expect(labels).toEqual(['重来 1 分钟', '困难 6 分钟', '良好 10 分钟', '简单 8 天']);
  });

  it('shows only the rating names when the render response has no intervals', async () => {
    const target = await setup(undefined);
    expect(target.querySelectorAll('[data-testid="review-rating-interval"]')).toHaveLength(0);
    expect(target.querySelectorAll('[data-rating]')).toHaveLength(4);
  });

  it('offers a how-to-choose help popover next to the ratings', async () => {
    const target = await setup([]);
    const trigger = target.querySelector('[data-testid="review-ratings-help"]');
    expect(trigger).not.toBeNull();
    expect(trigger?.getAttribute('aria-label')).toContain('怎么选评分');
  });
});
