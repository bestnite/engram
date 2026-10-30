/**
 * Pure decision functions for the review page's keyboard and touch gestures.
 *
 * The view only maps the decisions here into side effects (flip, grade, bury, suspend, jump to edit); DOM detection stays in the view.
 * It is split out so that "what a key/swipe does in each card state" can be exhaustively unit-tested in a browserless environment
 * (vitest's node environment) - real keys and gestures are still verified end to end in the browser.
 */

/** The action of one key press on the review page. */
export type ReviewShortcut =
  | { kind: 'ignore' } // Not handled, and the default behavior is not prevented
  | { kind: 'swallow' } // Prevent the default but produce no action (1–4 on a graded card awaiting an answer)
  | { kind: 'continue' } // Result panel: go to the next card
  | { kind: 'reveal' } // Space/Enter: request reveal on a graded card, flip a self-graded card
  | { kind: 'submit' } // Enter: submit the answer on a graded card awaiting one
  | { kind: 'give_up' } // Enter/Space: after the graded card's answer is revealed, record it as Again and move on
  | { kind: 'rate'; rating: number }
  | { kind: 'bury' }
  | { kind: 'suspend' }
  | { kind: 'undo' } // u: undo the last grade (including self-graded kinds)
  | { kind: 'edit' };

/** Review state the key decisions depend on. */
export interface ReviewShortcutState {
  /** Whether focus is on a text input control (in that case only Enter-submit for graded cards is handled). */
  typing: boolean;
  /**
   * Whether focus is on an options control (single/multiple choice). These controls are buttons with role=radio/checkbox, and Space is their
   * own "select" key; hijacking it as "show answer" would make answering impossible for keyboard users.
   */
  choosing: boolean;
  /** An IME is composing: Enter then confirms a candidate, it does not submit. */
  composing: boolean;
  /** Auto-repeat from holding a key down: repeated Enter would chain "submit" and "continue" into one key press, so it is always ignored. */
  repeat: boolean;
  /** Whether the result panel is showing, i.e. answered with only "continue" left. */
  feedback: boolean;
  /** Whether the server grades this card's answer (the due-card payload's machine_graded flag). */
  machineGraded: boolean;
  /** Whether the graded card's answer has been revealed. */
  gradedRevealed: boolean;
  /** Whether the self-graded card has been flipped. */
  revealed: boolean;
  /** Whether this session still has a submission to undo (when not, u is not swallowed and goes to the browser). */
  canUndo: boolean;
}

/**
 * Map one keydown to a review action.
 * Modifier keys (Alt/Ctrl/Meta), IME composition and auto-repeat are always ignored; with a text input focused only Enter-submit for graded cards is handled,
 * and other keys go to the input itself.
 */
export function reviewShortcut(
  key: string,
  modifiers: { alt: boolean; ctrl: boolean; meta: boolean },
  state: ReviewShortcutState,
): ReviewShortcut {
  if (modifiers.alt || modifiers.ctrl || modifiers.meta || state.composing || state.repeat) return { kind: 'ignore' };

  // Graded card awaiting an answer: Enter submits, whether focus is in the input, on the options or on the page - the usual form convention.
  const answering = state.machineGraded && !state.gradedRevealed && !state.feedback;
  if (answering && key === 'Enter') return { kind: 'submit' };

  if (state.typing) return { kind: 'ignore' };

  // The result panel is the "answered, only continue left" state: Enter/Space/1–4 all continue; e still edits; b does nothing.
  if (state.feedback) {
    if (key === ' ' || key === 'Enter' || /^[1-4]$/.test(key)) return { kind: 'continue' };
    if (key.toLowerCase() === 'e') return { kind: 'edit' };
    if (key.toLowerCase() === 'u') return state.canUndo ? { kind: 'undo' } : { kind: 'ignore' };
    return { kind: 'ignore' };
  }

  if (key === ' ' || key === 'Enter') {
    // Once the graded card's answer is revealed, the only way out is "record as Again and continue", which Enter/Space take directly.
    if (state.machineGraded && state.gradedRevealed) return { kind: 'give_up' };
    // When focus is on the options, Space is left to the options themselves to select.
    if (state.choosing) return { kind: 'ignore' };
    // Otherwise prevent the default: reveal the answer on a graded card, flip a self-graded card.
    return { kind: 'reveal' };
  }

  // While a graded card awaits an answer, 1–4 do nothing at all (no grading, no reveal).
  if (/^[1-4]$/.test(key)) {
    if (state.machineGraded) return { kind: 'swallow' };
    if (state.revealed) return { kind: 'rate', rating: Number(key) };
    return { kind: 'ignore' };
  }

  const lower = key.toLowerCase();
  if (lower === 'b') return { kind: 'bury' };
  if (lower === 's') return { kind: 'suspend' };
  if (lower === 'u') return state.canUndo ? { kind: 'undo' } : { kind: 'ignore' };
  if (lower === 'e') return { kind: 'edit' };
  return { kind: 'ignore' };
}

/** The action of one horizontal swipe on the review page. */
export type ReviewSwipe =
  | { kind: 'ignore' }
  | { kind: 'reveal' } // Before revealing, any horizontal swipe means "show answer"
  | { kind: 'rate'; rating: number }; // After revealing, swipe left for Again and right for Good

/** Review state the swipe decisions depend on. */
export interface ReviewSwipeState {
  feedback: boolean;
  machineGraded: boolean;
  gradedRevealed: boolean;
  revealed: boolean;
}

/** Horizontal swipe threshold (pixels; shared by the review view and the gesture decision). */
export const SWIPE_THRESHOLD = 45;

/**
 * Map one touchend displacement to a review action.
 * Not a swipe when the horizontal displacement is too small or the vertical one is larger; the result panel and a revealed graded card do not take swipes.
 */
export function reviewSwipe(dx: number, dy: number, state: ReviewSwipeState): ReviewSwipe {
  if (Math.abs(dx) < SWIPE_THRESHOLD || Math.abs(dx) < Math.abs(dy)) return { kind: 'ignore' };
  if (state.feedback) return { kind: 'ignore' };
  if (state.machineGraded) return state.gradedRevealed ? { kind: 'ignore' } : { kind: 'reveal' };
  if (!state.revealed) return { kind: 'reveal' };
  return { kind: 'rate', rating: dx < 0 ? 1 : 3 };
}
