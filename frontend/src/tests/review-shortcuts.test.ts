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
  return { typing: false, feedback: false, gradedKind: false, gradedRevealed: false, revealed: false, ...over };
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

  it('maps space/enter to reveal for both self-assessed and graded cards', () => {
    expect(reviewShortcut(' ', noMods, keyState())).toEqual({ kind: 'reveal' });
    expect(reviewShortcut('Enter', noMods, keyState())).toEqual({ kind: 'reveal' });
    expect(reviewShortcut(' ', noMods, keyState({ gradedKind: true }))).toEqual({ kind: 'reveal' });
    // 已翻面/已揭示时仍返回 reveal，视图侧自行判断为 no-op（保持 SSR 的 preventDefault 语义）。
    expect(reviewShortcut('Enter', noMods, keyState({ revealed: true }))).toEqual({ kind: 'reveal' });
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
