import { CARD_KIND_FIELDS } from './card-fields';

/**
 * 后端枚举 → 界面文案。
 *
 * 后端发的 `private` / `basic_both` 这类值是**机器词汇**：把它直接插进模板
 * （`<span>{deck.visibility}</span>`）就等于在中文界面里印英文——它和硬编码英文的结果
 * 一模一样，只是看起来不像硬编码，所以 grep 硬编码文案的门禁也抓不到它。
 *
 * 两条约定：
 *
 *  1. 判据是「这个串是给机器读还是给人读的」。进查询串、进请求体的必须是原值；凡是会
 *     出现在屏幕上的，一律先翻成语言包键。
 *  2. **未知值一律当成空**。`formatMessage` 找不到键时会**原样返回键名**，所以
 *     `$t('notes.kind.' + kind)` 在后端加了新题型时会印出 `notes.kind.foo`；直接插原值
 *     则印出机器词。两者都比「少一个徽标」糟：界面宁可暂时少显示一格，也不要谎报或
 *     吐内部标识——因为下一次改动会顺便把它补上，而误报的状态没人会发现。
 */

/** 卡组可见性的全部取值（与 internal/store 的 DeckVisibility* 常量一致）。 */
export const DECK_VISIBILITIES = ['private', 'unlisted', 'public'] as const;

/** 翻译函数的最小形状（`$t` 即可）。 */
export type Translate = (key: string) => string;

/** 卡组可见性的语言包键；未知/空值返回 null。 */
export function deckVisibilityLabelKey(value: string | null | undefined): string | null {
  if (!value || !(DECK_VISIBILITIES as readonly string[]).includes(value)) return null;
  return `deck.sharing.visibility.${value}`;
}

/** 题型的语言包键；未知/空值返回 null。题型清单以字段表为唯一来源。 */
export function noteKindLabelKey(kind: string | null | undefined): string | null {
  if (!kind || !(kind in CARD_KIND_FIELDS)) return null;
  return `notes.kind.${kind}`;
}

/** 卡组可见性徽标文案；未知枚举返回空串（调用方据此隐藏徽标）。 */
export function deckVisibilityLabel(value: string | null | undefined, t: Translate): string {
  const key = deckVisibilityLabelKey(value);
  return key ? t(key) : '';
}

/** 题型徽标文案；未知枚举返回空串。 */
export function noteKindLabel(kind: string | null | undefined, t: Translate): string {
  const key = noteKindLabelKey(kind);
  return key ? t(key) : '';
}

/**
 * 预设显示名。
 *
 * 默认预设的库内名字是稳定标识（`store.DefaultPresetName`，英文常量"Default"），
 * 它是**机器词汇**：直接印在中文界面上就是英文，与硬编码英文没有区别。凡是要显示在
 * 屏幕上的预设名都走这里，由服务端的 `is_default` 决定用语言包文案还是用户自己起的名字。
 */
export function presetDisplayName(name: string, isDefault: boolean, t: Translate): string {
  return isDefault ? t('presets.default_name') : name;
}
