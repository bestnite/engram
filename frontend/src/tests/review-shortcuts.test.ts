import { describe, it, expect } from 'vitest';
import { reviewShortcut, reviewSwipe, SWIPE_THRESHOLD } from '../lib/review-shortcuts';
import type { ReviewShortcutState, ReviewSwipeState } from '../lib/review-shortcuts';

/**
 * 复习页键位与滑动的行为回归。
 *
 * 这些用例穷举「每种卡片状态 × 每个按键/位移」的决策，补上无浏览器环境下无法触发的
 * 真实交互；浏览器端到端只再验证副作用确实接上（翻面、评分请求、埋藏请求）。
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
    // 焦点在输入控件上：空格、1–4、b、e 全不处理。
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
    // 已翻面/已揭示时仍返回 reveal，视图侧自行判断为 no-op（保持 SSR 的 preventDefault 语义）。
    expect(reviewShortcut('Enter', noMods, keyState({ revealed: true }))).toEqual({ kind: 'reveal' });
  });

  it('submits a graded answer with enter from the input, an option or the page', () => {
    const answering = keyState({ gradedKind: true });
    expect(reviewShortcut('Enter', noMods, { ...answering, typing: true })).toEqual({ kind: 'submit' });
    expect(reviewShortcut('Enter', noMods, { ...answering, choosing: true })).toEqual({ kind: 'submit' });
    expect(reviewShortcut('Enter', noMods, answering)).toEqual({ kind: 'submit' });
    // 输入框里其它按键仍交给输入框（空格是答案的一部分，不是「显示答案」）。
    expect(reviewShortcut(' ', noMods, { ...answering, typing: true })).toEqual({ kind: 'ignore' });
    // 输入框里的回车只对判分卡有意义：自评卡没有可提交的答案。
    expect(reviewShortcut('Enter', noMods, keyState({ typing: true }))).toEqual({ kind: 'ignore' });
  });

  it('never submits while an IME is composing, on auto-repeat, or with a modifier', () => {
    const answering = keyState({ gradedKind: true, typing: true });
    expect(reviewShortcut('Enter', noMods, { ...answering, composing: true })).toEqual({ kind: 'ignore' });
    expect(reviewShortcut('Enter', noMods, { ...answering, repeat: true })).toEqual({ kind: 'ignore' });
    expect(reviewShortcut('Enter', { alt: false, ctrl: true, meta: false }, answering)).toEqual({ kind: 'ignore' });
    // 按住回车不放：自动重复不能把「提交」之后的结果面板连带「继续」掉。
    const result = keyState({ feedback: true, gradedKind: true, gradedRevealed: true });
    expect(reviewShortcut('Enter', noMods, { ...result, repeat: true })).toEqual({ kind: 'ignore' });
  });

  it('leaves space to a focused choice option instead of revealing the answer', () => {
    expect(reviewShortcut(' ', noMods, keyState({ gradedKind: true, choosing: true }))).toEqual({ kind: 'ignore' });
    // 焦点不在选项上时空格仍是「显示答案」。
    expect(reviewShortcut(' ', noMods, keyState({ gradedKind: true }))).toEqual({ kind: 'reveal' });
  });

  it('gives up with enter or space once a graded answer is revealed', () => {
    const revealed = keyState({ gradedKind: true, gradedRevealed: true });
    expect(reviewShortcut('Enter', noMods, revealed)).toEqual({ kind: 'give_up' });
    expect(reviewShortcut(' ', noMods, revealed)).toEqual({ kind: 'give_up' });
    expect(reviewShortcut('3', noMods, revealed)).toEqual({ kind: 'swallow' });
  });

  it('gates 1-4: swallowed on unanswered graded cards, rating only after reveal', () => {
    // 自评卡未翻面：1–4 无效（也不阻止默认）。
    expect(reviewShortcut('1', noMods, keyState())).toEqual({ kind: 'ignore' });
    // 自评卡已翻面：1–4 直接评分。
    for (const rating of [1, 2, 3, 4]) {
      expect(reviewShortcut(String(rating), noMods, keyState({ revealed: true }))).toEqual({
        kind: 'rate',
        rating,
      });
    }
    // 判分卡待作答：1–4 被吞掉（阻止默认），绝不评分。
    expect(reviewShortcut('2', noMods, keyState({ gradedKind: true }))).toEqual({ kind: 'swallow' });
  });

  it('maps b to bury and e to edit on an active card', () => {
    expect(reviewShortcut('b', noMods, keyState())).toEqual({ kind: 'bury' });
    expect(reviewShortcut('B', noMods, keyState())).toEqual({ kind: 'bury' });
    expect(reviewShortcut('e', noMods, keyState())).toEqual({ kind: 'edit' });
    expect(reviewShortcut('E', noMods, keyState())).toEqual({ kind: 'edit' });
    // 判分卡待作答时同样可埋藏/编辑（按钮在生产路径里对所有未作答卡可见）。
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
    // 结果面板不再提供 b。
    expect(reviewShortcut('b', noMods, result)).toEqual({ kind: 'ignore' });
    expect(reviewShortcut('x', noMods, result)).toEqual({ kind: 'ignore' });
  });

  it('maps u to undo only when there is a submission to undo', () => {
    // 没有可撤销的提交时 u 不吞按键（交给浏览器）。
    expect(reviewShortcut('u', noMods, keyState())).toEqual({ kind: 'ignore' });
    expect(reviewShortcut('U', noMods, keyState({ gradedKind: true }))).toEqual({ kind: 'ignore' });
    // 有提交时：自评卡翻面后、判分结果面板上都能撤销（含自评题——它们没有结果面板）。
    expect(reviewShortcut('u', noMods, keyState({ revealed: true, canUndo: true }))).toEqual({ kind: 'undo' });
    expect(reviewShortcut('U', noMods, keyState({ gradedKind: true, canUndo: true }))).toEqual({ kind: 'undo' });
    const result = keyState({ feedback: true, gradedKind: true, gradedRevealed: true, canUndo: true });
    expect(reviewShortcut('u', noMods, result)).toEqual({ kind: 'undo' });
    expect(reviewShortcut('U', noMods, result)).toEqual({ kind: 'undo' });
    // 输入焦点上仍不处理。
    expect(reviewShortcut('u', noMods, keyState({ typing: true, canUndo: true }))).toEqual({ kind: 'ignore' });
  });
});

describe('reviewSwipe mapping', () => {
  function swipeState(over: Partial<ReviewSwipeState> = {}): ReviewSwipeState {
    return { feedback: false, gradedKind: false, gradedRevealed: false, revealed: false, ...over };
  }

  it('requires a horizontal displacement past the threshold', () => {
    // 位移不足。
    expect(reviewSwipe(SWIPE_THRESHOLD - 1, 0, swipeState())).toEqual({ kind: 'ignore' });
    // 垂直位移更大：不是横向滑动。
    expect(reviewSwipe(SWIPE_THRESHOLD + 30, SWIPE_THRESHOLD + 60, swipeState())).toEqual({
      kind: 'ignore',
    });
  });

  it('reveals on any horizontal swipe before reveal, then rates by direction', () => {
    // 未翻面：左右滑都是「显示答案」。
    expect(reviewSwipe(-120, 0, swipeState())).toEqual({ kind: 'reveal' });
    expect(reviewSwipe(120, 0, swipeState())).toEqual({ kind: 'reveal' });
    // 已翻面：左滑 Again(1)、右滑 Good(3)。
    expect(reviewSwipe(-120, 0, swipeState({ revealed: true }))).toEqual({ kind: 'rate', rating: 1 });
    expect(reviewSwipe(120, 0, swipeState({ revealed: true }))).toEqual({ kind: 'rate', rating: 3 });
  });

  it('never swipes the result panel or an already-revealed graded card', () => {
    expect(reviewSwipe(-120, 0, swipeState({ feedback: true }))).toEqual({ kind: 'ignore' });
    expect(reviewSwipe(-120, 0, swipeState({ gradedKind: true, gradedRevealed: true }))).toEqual({
      kind: 'ignore',
    });
    // 判分卡未揭示：滑动即请求揭示答案。
    expect(reviewSwipe(-120, 0, swipeState({ gradedKind: true }))).toEqual({ kind: 'reveal' });
  });
});
