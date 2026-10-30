import { describe, it, expect, afterEach, vi } from 'vitest';
import { mount, unmount, flushSync } from 'svelte';
import ReviewView from '../lib/views/ReviewView.svelte';
import type { ApiClient, DueCard } from '../lib/api';
import { setLocale } from '../lib/i18n';
import { faces } from './card-face-fixture';

/**
 * The empty review page marks how the learner got there: a session that rated at least one card
 * and cleared its queue shows a party popper, while a page that opened onto an empty queue shows
 * only a check mark. Both icons are decorative and stay hidden from screen readers.
 */
describe('ReviewView empty-state icon', () => {
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
    state: 'review',
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

  function mountReview(client: ApiClient): HTMLElement {
    setLocale('zh-CN');
    const target = document.createElement('div');
    document.body.appendChild(target);
    instance = mount(ReviewView, { target, props: { client } });
    return target;
  }

  function emptyState(target: HTMLElement): HTMLElement {
    const element = target.querySelector('[data-testid="review-empty"]') as HTMLElement | null;
    expect(element).not.toBeNull();
    return element as HTMLElement;
  }

  it('shows a check mark, not a celebration, when the queue was empty from the start', async () => {
    const client = {
      getDueCards: vi.fn().mockResolvedValue({ cards: [] }),
      getCardTypes: vi.fn(),
    } as unknown as ApiClient;
    const target = mountReview(client);
    await flush();
    const empty = emptyState(target);
    const icon = empty.querySelector('svg.lucide-circle-check-big');
    expect(icon).not.toBeNull();
    expect(icon?.getAttribute('aria-hidden')).toBe('true');
    expect(empty.querySelector('svg.lucide-party-popper')).toBeNull();
  });

  it('celebrates once the last due card of the session is rated', async () => {
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
      submitSelfReview: vi.fn().mockResolvedValue({ card_id: 'c1', state: 'review', version: 1, remaining: 0, cards: [] }),
    } as unknown as ApiClient;
    const target = mountReview(client);
    await flush();
    expect(target.querySelector('[data-testid="review-empty"]')).toBeNull();
    (target.querySelector('[data-testid="review-reveal"]') as HTMLElement).click();
    await flush();
    (target.querySelector('[data-rating="3"]') as HTMLElement).click();
    await flush();
    const empty = emptyState(target);
    const icon = empty.querySelector('svg.lucide-party-popper');
    expect(icon).not.toBeNull();
    expect(icon?.getAttribute('aria-hidden')).toBe('true');
    expect(empty.querySelector('svg.lucide-circle-check-big')).toBeNull();
  });
});
