import { CARD_KIND_FIELDS } from './card-fields';

/**
 * 后端枚举 → 界面文案。
 *
 * 后端发的 `basic_both` 这类值是**机器词汇**：把它直接插进模板
 * （`<span>{note.kind}</span>`）就等于在中文界面里印英文——它和硬编码英文的结果
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

/** 翻译函数的最小形状（`$t` 即可）。 */
export type Translate = (key: string) => string;

/** 题型的语言包键；未知/空值返回 null。题型清单以字段表为唯一来源。 */
export function noteKindLabelKey(kind: string | null | undefined): string | null {
  if (!kind || !(kind in CARD_KIND_FIELDS)) return null;
  return `notes.kind.${kind}`;
}

/**
 * 卡组卡片上的危险操作种类。
 * delete：自有卡组，删除不可逆；leave：被共享的卡组，退出只撤掉自己一行授权。
 */
export type DeckActionKind = 'delete' | 'leave';

/**
 * 依据调用者在该卡组上的显式关系（Deck.role）决定卡片上显示哪种操作。
 *
 * owner -> 'delete'；editor/reader（被共享）-> 'leave'。
 * 列表里的卡组必然属于这两类之一（自有，或被显式授权）——不存在「看得到但没有授权」的
 * 卡组。未知值一律返回 null：宁可少显示一个按钮，也不能给一个没有授权行的卡组渲染
 * 「退出共享」，那种卡组点了只会得到 not_found。
 */
export function deckActionKind(role: string | null | undefined): DeckActionKind | null {
  switch (role) {
    case 'owner':
      return 'delete';
    case 'editor':
    case 'reader':
      return 'leave';
    default:
      return null;
  }
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

/** 预设下拉里的一项；value 是预设 id 的字符串形式（Select 只接受字符串值）。 */
export interface PresetSelectOption {
  value: string;
  label: string;
}

/**
 * 创建卡组弹窗的预设下拉：把预设列表投影成选项，并给出应预选的值。
 *
 * 这里刻意**不**追加「默认预设」哨兵项。服务端的补齐保证列表里至少有一条默认预设，
 * 而它的显示名（presets.default_name）与哨兵项文案几乎同字：只有一条预设时下拉会同时
 * 出现两条「默认」，看着像一条没删掉的预设。
 *
 * 列表为空（拉取失败）时 selected 为空串，调用方据此按服务端默认预设提交（preset_id 为空串）。
 */
export function presetSelectOptions(
  presets: ReadonlyArray<{ id: string; name: string; is_default: boolean }>,
  t: Translate
): { options: PresetSelectOption[]; selected: string } {
  const options = presets.map((item) => ({
    value: String(item.id),
    label: presetDisplayName(item.name, item.is_default, t),
  }));
  const chosen = presets.find((item) => item.is_default) ?? presets[0];
  return { options, selected: chosen ? String(chosen.id) : '' };
}
