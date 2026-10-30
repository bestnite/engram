import { describe, it, expect, afterEach, beforeEach, vi } from 'vitest';
import { mount, unmount, flushSync } from 'svelte';
import ReviewView from '../lib/views/ReviewView.svelte';
import type { ApiClient, DueCard } from '../lib/api';
import { setLocale } from '../lib/i18n';
import { clearCardTypes } from './card-type-fixture';
import { faces } from './card-face-fixture';

/**
 * A note type created through POST /api/v1/decks/:id/note-types stores opaque field ids and matches no engram stock
 * kind, so the card-kind catalog has no entry for it. The review page answers it from the due-card payload alone:
 * `answer_control` and `machine_graded` choose the flow, and `field_order` orders the plain-text fallback.
 */
describe('ReviewView any note type', () => {
  let instance: ReturnType<typeof mount> | null = null;

  beforeEach(() => {
    // No catalog is loaded: the page must not need one.
    clearCardTypes();
  });

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

  /** A card of a note type the stock catalog does not describe: opaque field ids, an unknown template id, kind "". */
  function customCard(flow: Pick<DueCard, 'answer_control' | 'machine_graded'>, fields: Record<string, unknown>): DueCard {
    return {
      card_id: 'x1',
      note_id: 'y1',
      deck_id: 'd1',
      state: 'new',
      due_at: '',
      retrievability: 0,
      kind: '',
      fields,
      tags: [],
      template: 'card-1',
      version: 0,
      field_order: ['f-front', 'f-back'],
      ...flow,
    };
  }

  async function mountView(card: DueCard, client: Partial<ApiClient>): Promise<HTMLElement> {
    setLocale('zh-CN');
    const target = document.createElement('div');
    document.body.appendChild(target);
    instance = mount(ReviewView, {
      target,
      props: { client: { getDueCards: vi.fn().mockResolvedValue({ cards: [card] }), ...client } as unknown as ApiClient },
    });
    await flush();
    return target;
  }

  it('reaches reveal and rating with no catalog loaded', async () => {
    const card = customCard(
      { answer_control: null, machine_graded: false },
      { 'f-front': '自定义问题', 'f-back': '自定义答案' },
    );
    const getCardTypes = vi.fn();
    const submitSelfReview = vi.fn().mockResolvedValue({ card_id: 'x1', state: 'learning', version: 1, remaining: 0, cards: [] });
    const target = await mountView(card, {
      getCardTypes,
      renderReviewCard: vi.fn().mockResolvedValue({
        card_id: 'x1',
        ...faces('<p>自定义问题</p>', '<p>自定义答案</p>'),
        edit_href: '',
      }),
      submitSelfReview,
    });

    // The catalog is never requested: the flow came from the card payload.
    expect(getCardTypes).not.toHaveBeenCalled();
    expect(target.querySelector('[data-testid="review-front"]')).not.toBeNull();

    (target.querySelector('[data-testid="review-reveal"]') as HTMLElement).click();
    await flush();
    expect(target.querySelector('[data-testid="review-answer"]')).not.toBeNull();

    (target.querySelector('[data-rating="3"]') as HTMLElement).click();
    await flush();
    expect(submitSelfReview).toHaveBeenCalledWith(
      expect.objectContaining({ card_id: 'x1', rating: 3 }),
    );
  });

  it('lists the field values in the note type order when the face fails to load', async () => {
    const card = customCard(
      { answer_control: null, machine_graded: false },
      { 'f-front': 'FIRST-VALUE', 'f-back': 'SECOND-VALUE' },
    );
    const target = await mountView(card, { renderReviewCard: vi.fn().mockRejectedValue(new Error('render failed')) });

    // The question side is the first field; the answer side stays out until the reveal.
    expect(target.textContent).toContain('FIRST-VALUE');
    expect(target.textContent).not.toContain('SECOND-VALUE');

    (target.querySelector('[data-testid="review-reveal"]') as HTMLElement).click();
    await flush();
    const text = target.textContent ?? '';
    expect(text).toContain('FIRST-VALUE');
    expect(text).toContain('SECOND-VALUE');
    expect(text.indexOf('FIRST-VALUE')).toBeLessThan(text.indexOf('SECOND-VALUE'));
  });

  it('takes the machine-graded flow when the server grades the card', async () => {
    const card = customCard(
      { answer_control: 'text', machine_graded: true },
      { 'f-front': '自定义问题', 'f-back': '自定义答案' },
    );
    const target = await mountView(card, {
      renderReviewCard: vi.fn().mockResolvedValue({
        card_id: 'x1',
        ...faces('<p>自定义问题</p>', '<p>自定义答案</p>'),
        edit_href: '',
      }),
    });

    // The graded flow: submit and reveal buttons, and no self-rating keys.
    expect(target.querySelector('[data-testid="review-graded-prompt"]')).not.toBeNull();
    expect(target.querySelector('[data-testid="review-graded-submit"]')).not.toBeNull();
    expect(target.querySelector('[data-testid="review-graded-reveal"]')).not.toBeNull();
    expect(target.querySelector('[data-rating="1"]')).toBeNull();
  });
});
