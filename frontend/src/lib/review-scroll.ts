/**
 * Where the review page scrolls when a card's answer appears.
 *
 * Anki's reviewer scrolls the answer side to its `#answer` element. The card frame is as tall as its face and never
 * scrolls, so the review page scrolls the window instead. An answer without such an anchor has no place Anki would jump
 * to: the page then scrolls only when the answer starts outside the visible area, because a learner who already sees the
 * start of the answer would only lose the question above it.
 */

/** Space kept between the viewport's top edge and the scroll target. */
export const ANSWER_SCROLL_MARGIN = 16;

/** How many pixels of the answer must show above the pinned action bar for its start to count as visible. */
export const ANSWER_VISIBLE_MIN = 48;

export interface AnswerScrollInput {
  /** Viewport y of the face's `#answer` element; null when the answer has no anchor to jump to. */
  anchorTop: number | null;
  /** Viewport y of the top of the answer area. */
  startTop: number;
  /** The window's current vertical scroll offset. */
  scrollY: number;
  /** The viewport's height. */
  viewportHeight: number;
  /** Height of the action bar pinned to the viewport's bottom edge; the page under it is covered. */
  barHeight: number;
}

/** The window scroll offset that brings the answer into view, or null when the page should stay where it is. */
export function answerScrollTop(input: AnswerScrollInput): number | null {
  const { anchorTop, startTop, scrollY, viewportHeight, barHeight } = input;
  if (anchorTop !== null) return Math.max(0, Math.round(scrollY + anchorTop - ANSWER_SCROLL_MARGIN));
  const visibleBottom = viewportHeight - barHeight;
  if (startTop >= 0 && startTop <= visibleBottom - ANSWER_VISIBLE_MIN) return null;
  return Math.max(0, Math.round(scrollY + startTop - ANSWER_SCROLL_MARGIN));
}
