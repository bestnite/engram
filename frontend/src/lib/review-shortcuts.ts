/**
 * 复习页键盘与触屏手势的纯决策函数。
 *
 * 视图只负责把这里的决策映射成副作用（翻面、评分、埋藏、跳转编辑），DOM 判定留在视图里。
 * 拆出来是为了让「按键/滑动在每种卡片状态下该做什么」能在无浏览器环境（vitest 的 node
 * 环境）里被穷举单测——真实按键与手势仍由浏览器端到端验证。
 */

/** 一次按键在复习页上的动作。 */
export type ReviewShortcut =
  | { kind: 'ignore' } // 不处理，也不阻止默认行为
  | { kind: 'swallow' } // 阻止默认行为但不产生动作（判分卡待作答时的 1–4）
  | { kind: 'continue' } // 结果面板：进入下一张
  | { kind: 'reveal' } // 空格/回车：判分卡请求揭示、自评卡翻面
  | { kind: 'rate'; rating: number }
  | { kind: 'bury' }
  | { kind: 'edit' };

/** 按键决策依赖的复习状态。 */
export interface ReviewShortcutState {
  /** 焦点是否落在输入控件上（此时一律不处理快捷键）。 */
  typing: boolean;
  /** 是否处于「已作答、只差继续」的结果面板。 */
  feedback: boolean;
  /** 当前卡是否为判分题型。 */
  gradedKind: boolean;
  /** 判分卡是否已揭示答案。 */
  gradedRevealed: boolean;
  /** 自评卡是否已翻面。 */
  revealed: boolean;
}

/**
 * 把一次 keydown 映射成复习动作。
 * 修饰键（Alt/Ctrl/Meta）或输入焦点一律忽略；其余按键都映射到复习动作。
 */
export function reviewShortcut(
  key: string,
  modifiers: { alt: boolean; ctrl: boolean; meta: boolean },
  state: ReviewShortcutState,
): ReviewShortcut {
  if (modifiers.alt || modifiers.ctrl || modifiers.meta || state.typing) return { kind: 'ignore' };

  // 结果面板是「已作答、只差继续」的状态：回车/空格/1–4 都走「继续」；e 仍可编辑；b 无效。
  if (state.feedback) {
    if (key === ' ' || key === 'Enter' || /^[1-4]$/.test(key)) return { kind: 'continue' };
    if (key.toLowerCase() === 'e') return { kind: 'edit' };
    return { kind: 'ignore' };
  }

  // 空格/回车始终阻止默认行为：判分卡揭示答案，自评卡翻面。
  if (key === ' ' || key === 'Enter') return { kind: 'reveal' };

  // 判分卡待作答时 1–4 完全失效（不评分、也不揭示答案）。
  if (/^[1-4]$/.test(key)) {
    if (state.gradedKind) return { kind: 'swallow' };
    if (state.revealed) return { kind: 'rate', rating: Number(key) };
    return { kind: 'ignore' };
  }

  const lower = key.toLowerCase();
  if (lower === 'b') return { kind: 'bury' };
  if (lower === 'e') return { kind: 'edit' };
  return { kind: 'ignore' };
}

/** 一次横向滑动在复习页上的动作。 */
export type ReviewSwipe =
  | { kind: 'ignore' }
  | { kind: 'reveal' } // 未揭示时任一横向滑动都是「显示答案」
  | { kind: 'rate'; rating: number }; // 已揭示后左滑 Again、右滑 Good

/** 滑动决策依赖的复习状态。 */
export interface ReviewSwipeState {
  feedback: boolean;
  gradedKind: boolean;
  gradedRevealed: boolean;
  revealed: boolean;
}

/** 横向滑动阈值（像素；复习视图与手势判定共用）。 */
export const SWIPE_THRESHOLD = 45;

/**
 * 把一次 touchend 的位移映射成复习动作。
 * 水平位移不足或垂直位移更大时不构成滑动；结果面板与已揭示的判分卡不参与滑动。
 */
export function reviewSwipe(dx: number, dy: number, state: ReviewSwipeState): ReviewSwipe {
  if (Math.abs(dx) < SWIPE_THRESHOLD || Math.abs(dx) < Math.abs(dy)) return { kind: 'ignore' };
  if (state.feedback) return { kind: 'ignore' };
  if (state.gradedKind) return state.gradedRevealed ? { kind: 'ignore' } : { kind: 'reveal' };
  if (!state.revealed) return { kind: 'reveal' };
  return { kind: 'rate', rating: dx < 0 ? 1 : 3 };
}
