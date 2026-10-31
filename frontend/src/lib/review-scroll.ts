/**
 * Where the review page scrolls when a card's answer appears.
 *
 * Anki's reviewer scrolls the answer side to its `#answer` element. The card frame is as tall as its face and never
 * scrolls, so the review page scrolls the window instead. An answer without such an anchor has no place Anki would jump
 * to: the page then scrolls only when the answer starts outside the visible area, because a learner who already sees the
 * start of the answer would only lose the question above it.
 *
 * The jump is a preference, not a fixed rule: a learner can turn it off on the review page, and the choice is kept in
 * this browser (see `storedAnswerScroll`). With it off, the page never moves when an answer appears.
 */

/** Space kept between the viewport's top edge and the scroll target. */
export const ANSWER_SCROLL_MARGIN = 16;

/** Storage key of the learner's choice about scrolling to the answer. */
export const ANSWER_SCROLL_STORAGE_KEY = 'engram-review-answer-scroll';

/**
 * Get a usable localStorage; returns null when unavailable (private mode, non-browser environment).
 *
 * It is fetched explicitly through `window` instead of using the bare global identifier: some test environments
 * leave the bare `localStorage` undefined while `window.localStorage` is the object the host provides, and the
 * server-side rendering tests render this page with no `window` at all.
 */
function store(): Storage | null {
  try {
    if (typeof window !== 'undefined' && window.localStorage) {
      return window.localStorage;
    }
  } catch {
    // Some browsers throw on merely accessing this property in private mode.
  }
  return null;
}

/**
 * Whether the page should scroll to the answer when it appears; on unless the learner turned it off.
 *
 * Only the exact value `'0'` means off: a missing entry, an unreadable storage and any other value keep the
 * behaviour every existing learner already had, so a browser that cannot store the choice still brings the answer
 * into view.
 */
export function storedAnswerScroll(): boolean {
  try {
    return store()?.getItem(ANSWER_SCROLL_STORAGE_KEY) !== '0';
  } catch {
    return true;
  }
}

/** Remember the learner's choice; when storage is unavailable it still applies for this session, it is just not kept. */
export function setAnswerScroll(enabled: boolean): void {
  try {
    store()?.setItem(ANSWER_SCROLL_STORAGE_KEY, enabled ? '1' : '0');
  } catch {
    // Private mode: the toggle keeps working for this page, the choice simply does not survive a reload.
  }
}

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
