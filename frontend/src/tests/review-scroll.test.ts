import { describe, it, expect } from 'vitest';
import {
  answerScrollTop,
  ANSWER_SCROLL_MARGIN,
  ANSWER_VISIBLE_MIN,
  setAnswerScroll,
  storedAnswerScroll,
  type AnswerScrollInput,
} from '../lib/review-scroll';

/**
 * Where the review page scrolls when an answer appears. A face's `#answer` always decides, as in Anki's reviewer; an answer
 * without one moves the page only when its start is outside the visible strip above the pinned action bar.
 */

function input(over: Partial<AnswerScrollInput> = {}): AnswerScrollInput {
  return { anchorTop: null, startTop: 300, scrollY: 0, viewportHeight: 800, barHeight: 100, ...over };
}

describe('answerScrollTop', () => {
  const visibleBottom = 800 - 100;
  const cases: Array<{ name: string; over: Partial<AnswerScrollInput>; want: number | null }> = [
    { name: 'anchor below the fold scrolls to the anchor', over: { anchorTop: 1500, scrollY: 200 }, want: 200 + 1500 - ANSWER_SCROLL_MARGIN },
    { name: 'anchor already in view still scrolls to it', over: { anchorTop: 300 }, want: 300 - ANSWER_SCROLL_MARGIN },
    { name: 'anchor above the viewport scrolls up to it', over: { anchorTop: -400, scrollY: 1000 }, want: 1000 - 400 - ANSWER_SCROLL_MARGIN },
    { name: 'anchor near the page top never goes below zero', over: { anchorTop: 4 }, want: 0 },
    { name: 'anchor wins over a visible answer start', over: { anchorTop: 900, startTop: 100 }, want: 900 - ANSWER_SCROLL_MARGIN },
    { name: 'no anchor and a visible start stays put', over: { startTop: 300 }, want: null },
    { name: 'no anchor and a start at the viewport top stays put', over: { startTop: 0 }, want: null },
    { name: 'no anchor and the start at the last visible line stays put', over: { startTop: visibleBottom - ANSWER_VISIBLE_MIN }, want: null },
    {
      name: 'no anchor and too little of the answer above the bar scrolls',
      over: { startTop: visibleBottom - ANSWER_VISIBLE_MIN + 1, scrollY: 50 },
      want: 50 + visibleBottom - ANSWER_VISIBLE_MIN + 1 - ANSWER_SCROLL_MARGIN,
    },
    { name: 'no anchor and the start below the fold scrolls', over: { startTop: 2000, scrollY: 100 }, want: 100 + 2000 - ANSWER_SCROLL_MARGIN },
    { name: 'no anchor and the start above the viewport scrolls up', over: { startTop: -600, scrollY: 900 }, want: 900 - 600 - ANSWER_SCROLL_MARGIN },
    { name: 'no bar leaves the whole viewport visible', over: { startTop: 740, barHeight: 0 }, want: null },
  ];

  for (const { name, over, want } of cases) {
    it(name, () => {
      expect(answerScrollTop(input(over))).toBe(want);
    });
  }
});

/**
 * The learner's toggle. This suite has no `window`, which is exactly how the page is rendered on the server: the answer
 * must still be brought into view there, and toggling a control that has nowhere to write must not raise.
 */
describe('answer scroll preference', () => {
  it('is on when no browser storage exists', () => {
    expect(storedAnswerScroll()).toBe(true);
  });

  it('accepts a write without a store and keeps the default', () => {
    expect(() => setAnswerScroll(false)).not.toThrow();
    expect(storedAnswerScroll()).toBe(true);
  });
});
