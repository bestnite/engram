import { describe, it, expect, afterEach, vi } from 'vitest';
import { mount, unmount, flushSync } from 'svelte';
import ReviewView from '../lib/views/ReviewView.svelte';
import type { ApiClient, DueCard } from '../lib/api';
import { setLocale } from '../lib/i18n';
import { faces } from './card-face-fixture';

/**
 * After flipping a self-rated card, the rating row offers a "how to choose" help popover
 * beside the four buttons. The buttons carry only their rating names; no next-interval hint
 * is rendered under them.
 */
describe('ReviewView rating guidance', () => {
  let instance: ReturnType<typeof mount> | null = null;

  afterEach(() => {
    if (instance) unmount(instance);
    instance = null;
    document.body.innerHTML = '';
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
    answer_control: null,
    machine_graded: false,
    field_order: ['front', 'back'],
  };

  async function setup() {
    setLocale('zh-CN');
    const client = {
      getDueCards: vi.fn().mockResolvedValue({ cards: [card] }),
      renderReviewCard: vi.fn().mockResolvedValue({
        card_id: 'c1',
        ...faces('<p>q</p>', '<p>a</p>'),
        front_html: '<p>q</p>',
        back_html: '<p>a</p>',
        blanks: null,
        edit_href: '',
      }),
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

  it('shows only the rating names under the four buttons', async () => {
    const target = await setup();
    const labels = Array.from(target.querySelectorAll('[data-rating]')).map((el) => el.textContent?.replace(/\s+/g, ' ').trim());
    expect(labels).toEqual(['重来', '困难', '良好', '简单']);
  });

  it('offers a how-to-choose help popover next to the ratings', async () => {
    const target = await setup();
    const trigger = target.querySelector('[data-testid="review-ratings-help"]');
    expect(trigger).not.toBeNull();
    expect(trigger?.getAttribute('aria-label')).toContain('怎么选评分');
  });
});
