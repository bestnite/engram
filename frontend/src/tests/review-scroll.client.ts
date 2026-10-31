import { describe, it, expect, afterEach, vi } from 'vitest';
import { mount, unmount, flushSync } from 'svelte';
import ReviewView from '../lib/views/ReviewView.svelte';
import type { ApiClient, DueCard } from '../lib/api';
import { setLocale } from '../lib/i18n';
import { faces } from './card-face-fixture';
import { shownFaces, reportShown } from './card-frame-fixture';
import { ANSWER_SCROLL_MARGIN } from '../lib/review-scroll';

/**
 * How the review page scrolls around a long card: a new card starts at the page top, the revealed answer is brought into
 * view (to `#answer` when the face has one), and the hidden, empty answer frame never moves the page. The rule itself is
 * covered by review-scroll.test.ts; these cases pin the wiring between the frame's report and the window.
 */
describe('ReviewView scrolling', () => {
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

  function card(id: string): DueCard {
    return {
      card_id: id,
      note_id: 'n1',
      deck_id: 'd1',
      state: 'review',
      due_at: '',
      retrievability: 0,
      fields: { Front: 'Q', Back: 'A' },
      tags: [],
      template: 't0',
      version: 0,
      answer_control: null,
      machine_graded: false,
      field_order: ['Front', 'Back'],
    };
  }

  /** Layout the test environment does not compute: the answer frame's and answer area's tops, and the action bar's height. */
  function layout(rects: { answerFrameTop: number; answerAreaTop: number; barHeight: number }): void {
    vi.spyOn(Element.prototype, 'getBoundingClientRect').mockImplementation(function (this: Element) {
      let top = 0;
      let height = 0;
      if (this instanceof HTMLIFrameElement && this.getAttribute('data-side') === 'answer') top = rects.answerFrameTop;
      if (this.getAttribute('data-testid') === 'review-answer') top = rects.answerAreaTop;
      if (this.getAttribute('data-testid') === 'review-action-bar') height = rects.barHeight;
      return { top, height, bottom: top + height, left: 0, right: 0, width: 0, x: 0, y: top, toJSON: () => ({}) } as DOMRect;
    });
  }

  async function mountView(answerHtml: string, client: Partial<ApiClient> = {}): Promise<{ target: HTMLElement; scrollTo: ReturnType<typeof vi.fn> }> {
    setLocale('zh-CN');
    const scrollTo = vi.fn();
    vi.spyOn(window, 'scrollTo').mockImplementation(scrollTo as unknown as typeof window.scrollTo);
    const target = document.createElement('div');
    document.body.appendChild(target);
    instance = mount(ReviewView, {
      target,
      props: {
        client: {
          getDueCards: vi.fn().mockResolvedValue({ cards: [card('c1')] }),
          renderReviewCard: vi.fn().mockResolvedValue({
            card_id: 'c1',
            ...faces('<p>Q</p>', answerHtml, '/fm/token/'),
            answer_includes_question: true,
            edit_href: '/decks/d1/notes/n1',
          }),
          ...client,
        } as unknown as ApiClient,
      },
    });
    await flush();
    await shownFaces(target);
    return { target, scrollTo };
  }

  async function reveal(target: HTMLElement): Promise<void> {
    (target.querySelector('[data-testid="review-reveal"]') as HTMLElement).click();
    await flush();
    await shownFaces(target);
  }

  it('starts a card at the page top', async () => {
    const { scrollTo } = await mountView('<p>A</p>');
    expect(scrollTo).toHaveBeenCalledWith({ top: 0 });
  });

  it('scrolls a new card to the page top after a rating', async () => {
    const submitSelfReview = vi.fn().mockResolvedValue({ cards: [card('c2')], remaining: 1, version: 1 });
    const { target, scrollTo } = await mountView('<p>A</p>', { submitSelfReview });
    await reveal(target);
    scrollTo.mockClear();

    (target.querySelector('[data-rating="3"]') as HTMLElement).click();
    await flush();
    expect(submitSelfReview).toHaveBeenCalled();
    expect(scrollTo).toHaveBeenCalledWith({ top: 0 });
  });

  it('scrolls the revealed answer to the face #answer element', async () => {
    const { target, scrollTo } = await mountView('<p>Q</p><hr id=answer><p>A</p>');
    layout({ answerFrameTop: 120, answerAreaTop: 120, barHeight: 100 });
    await reveal(target);
    scrollTo.mockClear();

    await reportShown(target, 'answer', { answerTop: 900 });
    expect(scrollTo).toHaveBeenCalledWith({ top: 120 + 900 - ANSWER_SCROLL_MARGIN });
  });

  it('leaves the page alone when an answer without #answer already starts in view', async () => {
    const { target, scrollTo } = await mountView('<p>A</p>');
    layout({ answerFrameTop: 200, answerAreaTop: 200, barHeight: 100 });
    await reveal(target);
    scrollTo.mockClear();

    await reportShown(target, 'answer', { answerTop: null });
    expect(scrollTo).not.toHaveBeenCalled();
  });

  it('brings an answer without #answer into view when it starts below the fold', async () => {
    const { target, scrollTo } = await mountView('<p>A</p>');
    const below = window.innerHeight + 300;
    layout({ answerFrameTop: below, answerAreaTop: below, barHeight: 100 });
    await reveal(target);
    scrollTo.mockClear();

    await reportShown(target, 'answer', { answerTop: null });
    expect(scrollTo).toHaveBeenCalledWith({ top: below - ANSWER_SCROLL_MARGIN });
  });

  it('ignores the report of the hidden, empty answer face before the reveal', async () => {
    const { target, scrollTo } = await mountView('<p>Q</p><hr id=answer><p>A</p>');
    layout({ answerFrameTop: 120, answerAreaTop: 120, barHeight: 100 });
    scrollTo.mockClear();

    await reportShown(target, 'answer', { answerTop: 900 });
    expect(scrollTo).not.toHaveBeenCalled();
  });

  it('keeps the step buttons in the pinned action bar and the exit link in the status row', async () => {
    const { target } = await mountView('<p>A</p>');
    const bar = () => target.querySelector('[data-testid="review-action-bar"]') as HTMLElement;
    expect(bar().classList.contains('sticky')).toBe(true);
    expect(bar().querySelector('[data-testid="review-reveal"]')).not.toBeNull();

    await reveal(target);
    expect(bar().querySelectorAll('[data-rating]')).toHaveLength(4);

    const exit = target.querySelector('[data-testid="review-exit"]') as HTMLAnchorElement;
    expect(exit.getAttribute('href')).toBe('/');
    expect(exit.getAttribute('aria-label')).not.toBe('');
  });
});
