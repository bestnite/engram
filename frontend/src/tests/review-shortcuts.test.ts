import { describe, it, expect } from 'vitest';
import { reviewShortcut, reviewSwipe, SWIPE_THRESHOLD } from '../lib/review-shortcuts';
import type { ReviewShortcutState, ReviewSwipeState } from '../lib/review-shortcuts';

/**
 * Behaviour regression for the review page's key bindings and swipes.
 *
 * These cases exhaust the "card state × key/displacement" decision, filling in the real
 * interactions that cannot be triggered without a browser; the browser end-to-end only
 * re-verifies that the side effects are really wired up (flipping, rating, bury requests).
 */

const noMods = { alt: false, ctrl: false, meta: false };

function keyState(over: Partial<ReviewShortcutState> = {}): ReviewShortcutState {
  return {
    typing: false,
    choosing: false,
    composing: false,
    repeat: false,
    feedback: false,
    gradedKind: false,
    gradedRevealed: false,
    revealed: false,
    canUndo: false,
    ...over,
  };
}

describe('reviewShortcut keyboard mapping', () => {
  it('ignores modified keys and keystrokes typed into inputs', () => {
    for (const mods of [
      { alt: true, ctrl: false, meta: false },
      { alt: false, ctrl: true, meta: false },
      { alt: false, ctrl: false, meta: true },
    ]) {
      expect(reviewShortcut('b', mods, keyState())).toEqual({ kind: 'ignore' });
    }
    // Focus is on an input control: space, 1–4, b and e are all ignored.
    const typing = keyState({ typing: true, revealed: true });
    expect(reviewShortcut(' ', noMods, typing)).toEqual({ kind: 'ignore' });
    expect(reviewShortcut('3', noMods, typing)).toEqual({ kind: 'ignore' });
    expect(reviewShortcut('b', noMods, typing)).toEqual({ kind: 'ignore' });
    expect(reviewShortcut('e', noMods, typing)).toEqual({ kind: 'ignore' });
  });

  it('maps space/enter to reveal on self-assessed cards and space to reveal on graded cards', () => {
    expect(reviewShortcut(' ', noMods, keyState())).toEqual({ kind: 'reveal' });
    expect(reviewShortcut('Enter', noMods, keyState())).toEqual({ kind: 'reveal' });
    expect(reviewShortcut(' ', noMods, keyState({ gradedKind: true }))).toEqual({ kind: 'reveal' });
    // Already flipped/revealed still returns reveal; the view decides it is a no-op (preserving SSR's preventDefault semantics).
    expect(reviewShortcut('Enter', noMods, keyState({ revealed: true }))).toEqual({ kind: 'reveal' });
  });

  it('submits a graded answer with enter from the input, an option or the page', () => {
    const answering = keyState({ gradedKind: true });
    expect(reviewShortcut('Enter', noMods, { ...answering, typing: true })).toEqual({ kind: 'submit' });
    expect(reviewShortcut('Enter', noMods, { ...answering, choosing: true })).toEqual({ kind: 'submit' });
    expect(reviewShortcut('Enter', noMods, answering)).toEqual({ kind: 'submit' });
    // Other keys inside the input still go to the input (space is part of the answer, not "show answer").
    expect(reviewShortcut(' ', noMods, { ...answering, typing: true })).toEqual({ kind: 'ignore' });
    // Enter inside the input only matters for graded cards: a self-rated card has no answer to submit.
    expect(reviewShortcut('Enter', noMods, keyState({ typing: true }))).toEqual({ kind: 'ignore' });
  });

  it('never submits while an IME is composing, on auto-repeat, or with a modifier', () => {
    const answering = keyState({ gradedKind: true, typing: true });
    expect(reviewShortcut('Enter', noMods, { ...answering, composing: true })).toEqual({ kind: 'ignore' });
    expect(reviewShortcut('Enter', noMods, { ...answering, repeat: true })).toEqual({ kind: 'ignore' });
    expect(reviewShortcut('Enter', { alt: false, ctrl: true, meta: false }, answering)).toEqual({ kind: 'ignore' });
    // Holding Enter down: auto-repeat must not carry the result panel past "submit" into "continue".
    const result = keyState({ feedback: true, gradedKind: true, gradedRevealed: true });
    expect(reviewShortcut('Enter', noMods, { ...result, repeat: true })).toEqual({ kind: 'ignore' });
  });

  it('leaves space to a focused choice option instead of revealing the answer', () => {
    expect(reviewShortcut(' ', noMods, keyState({ gradedKind: true, choosing: true }))).toEqual({ kind: 'ignore' });
    // With focus not on an option, space is still "show answer".
    expect(reviewShortcut(' ', noMods, keyState({ gradedKind: true }))).toEqual({ kind: 'reveal' });
  });

  it('gives up with enter or space once a graded answer is revealed', () => {
    const revealed = keyState({ gradedKind: true, gradedRevealed: true });
    expect(reviewShortcut('Enter', noMods, revealed)).toEqual({ kind: 'give_up' });
    expect(reviewShortcut(' ', noMods, revealed)).toEqual({ kind: 'give_up' });
    expect(reviewShortcut('3', noMods, revealed)).toEqual({ kind: 'swallow' });
  });

  it('gates 1-4: swallowed on unanswered graded cards, rating only after reveal', () => {
    // Self-rated card not yet flipped: 1–4 do nothing (and do not preventDefault either).
    expect(reviewShortcut('1', noMods, keyState())).toEqual({ kind: 'ignore' });
    // Self-rated card flipped: 1–4 rate directly.
    for (const rating of [1, 2, 3, 4]) {
      expect(reviewShortcut(String(rating), noMods, keyState({ revealed: true }))).toEqual({
        kind: 'rate',
        rating,
      });
    }
    // Graded card awaiting an answer: 1–4 are swallowed (default prevented) and never rate.
    expect(reviewShortcut('2', noMods, keyState({ gradedKind: true }))).toEqual({ kind: 'swallow' });
  });

  it('maps b to bury and e to edit on an active card', () => {
    expect(reviewShortcut('b', noMods, keyState())).toEqual({ kind: 'bury' });
    expect(reviewShortcut('B', noMods, keyState())).toEqual({ kind: 'bury' });
    expect(reviewShortcut('e', noMods, keyState())).toEqual({ kind: 'edit' });
    expect(reviewShortcut('E', noMods, keyState())).toEqual({ kind: 'edit' });
    // While a graded card awaits an answer it can likewise be buried/edited (in the production path the buttons are visible on every unanswered card).
    expect(reviewShortcut('b', noMods, keyState({ gradedKind: true }))).toEqual({ kind: 'bury' });
  });

  it('maps s to suspend on an active card, but not on the result panel', () => {
    expect(reviewShortcut('s', noMods, keyState())).toEqual({ kind: 'suspend' });
    expect(reviewShortcut('S', noMods, keyState({ gradedKind: true }))).toEqual({ kind: 'suspend' });
    expect(reviewShortcut('s', noMods, keyState({ feedback: true }))).toEqual({ kind: 'ignore' });
    expect(reviewShortcut('s', noMods, keyState({ typing: true }))).toEqual({ kind: 'ignore' });
  });

  it('treats the result panel as continue-only plus edit', () => {
    const result = keyState({ feedback: true, gradedKind: true, gradedRevealed: true });
    for (const key of [' ', 'Enter', '1', '2', '3', '4']) {
      expect(reviewShortcut(key, noMods, result)).toEqual({ kind: 'continue' });
    }
    expect(reviewShortcut('e', noMods, result)).toEqual({ kind: 'edit' });
    // The result panel no longer offers b.
    expect(reviewShortcut('b', noMods, result)).toEqual({ kind: 'ignore' });
    expect(reviewShortcut('x', noMods, result)).toEqual({ kind: 'ignore' });
  });

  it('maps u to undo only when there is a submission to undo', () => {
    // With nothing undoable, u does not swallow the key (it goes to the browser).
    expect(reviewShortcut('u', noMods, keyState())).toEqual({ kind: 'ignore' });
    expect(reviewShortcut('U', noMods, keyState({ gradedKind: true }))).toEqual({ kind: 'ignore' });
    // With a submit available: undo works after flipping a self-rated card and on a graded result panel (self-rated questions included — they have no result panel).
    expect(reviewShortcut('u', noMods, keyState({ revealed: true, canUndo: true }))).toEqual({ kind: 'undo' });
    expect(reviewShortcut('U', noMods, keyState({ gradedKind: true, canUndo: true }))).toEqual({ kind: 'undo' });
    const result = keyState({ feedback: true, gradedKind: true, gradedRevealed: true, canUndo: true });
    expect(reviewShortcut('u', noMods, result)).toEqual({ kind: 'undo' });
    expect(reviewShortcut('U', noMods, result)).toEqual({ kind: 'undo' });
    // Still ignored while an input has focus.
    expect(reviewShortcut('u', noMods, keyState({ typing: true, canUndo: true }))).toEqual({ kind: 'ignore' });
  });
});

describe('reviewSwipe mapping', () => {
  function swipeState(over: Partial<ReviewSwipeState> = {}): ReviewSwipeState {
    return { feedback: false, gradedKind: false, gradedRevealed: false, revealed: false, ...over };
  }

  it('requires a horizontal displacement past the threshold', () => {
    // Displacement too small.
    expect(reviewSwipe(SWIPE_THRESHOLD - 1, 0, swipeState())).toEqual({ kind: 'ignore' });
    // Larger vertical displacement: not a horizontal swipe.
    expect(reviewSwipe(SWIPE_THRESHOLD + 30, SWIPE_THRESHOLD + 60, swipeState())).toEqual({
      kind: 'ignore',
    });
  });

  it('reveals on any horizontal swipe before reveal, then rates by direction', () => {
    // Not flipped: swiping either way is "show answer".
    expect(reviewSwipe(-120, 0, swipeState())).toEqual({ kind: 'reveal' });
    expect(reviewSwipe(120, 0, swipeState())).toEqual({ kind: 'reveal' });
    // Flipped: swipe left is Again(1), swipe right is Good(3).
    expect(reviewSwipe(-120, 0, swipeState({ revealed: true }))).toEqual({ kind: 'rate', rating: 1 });
    expect(reviewSwipe(120, 0, swipeState({ revealed: true }))).toEqual({ kind: 'rate', rating: 3 });
  });

  it('never swipes the result panel or an already-revealed graded card', () => {
    expect(reviewSwipe(-120, 0, swipeState({ feedback: true }))).toEqual({ kind: 'ignore' });
    expect(reviewSwipe(-120, 0, swipeState({ gradedKind: true, gradedRevealed: true }))).toEqual({
      kind: 'ignore',
    });
    // Graded card not yet revealed: a swipe requests revealing the answer.
    expect(reviewSwipe(-120, 0, swipeState({ gradedKind: true }))).toEqual({ kind: 'reveal' });
  });
});
