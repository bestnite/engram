import { describe, it, expect } from 'vitest';
import { formatMessage } from '../lib/i18n';
import {
  noteKindLabel,
  noteKindLabelKey,
  presetDisplayName,
  presetSelectOptions,
} from '../lib/labels';
import { createCatalog } from '../lib/card-types';
import { CARD_TYPES } from './card-type-fixture';

const catalog = createCatalog(CARD_TYPES.kinds);

/**
 * 枚举文案映射：后端枚举值一律经 lib/labels.ts 翻成语言包文案，未知值渲染成空。
 *
 * 判据不是「有没有引号」，而是「这个串是给机器读还是给人读的」——模板里直接插原值
 * （`{note.kind}`）在中文界面里会印出 `basic_both`，与硬编码英文同害，却不容易看出来。
 */

const zh = (key: string) => formatMessage('zh-CN', key);
const en = (key: string) => formatMessage('en', key);

describe('enum labels go through the catalog', () => {
  it('translates card kinds instead of printing the enum', () => {
    expect(noteKindLabel('basic_both', catalog, zh)).toBe('双向问答');
    expect(noteKindLabel('cloze', catalog, zh)).toBe('填空');
    expect(noteKindLabel('basic', catalog, en)).toBe('Basic');
  });

  it('renders nothing for an unknown or missing value', () => {
    // 关键：未知值绝不能回落到键名或机器词。$t 找不到键时会原样返回键名，
    // 直接插原值会印英文——两者都比「少一格徽标」糟。
    expect(noteKindLabel('made_up_kind', catalog, zh)).toBe('');
    expect(noteKindLabel(null, catalog, zh)).toBe('');
    expect(noteKindLabel(undefined, catalog, zh)).toBe('');
    // 元数据未就绪时也没有题型清单可用，同样返回空而不是裸 key。
    expect(noteKindLabel('cloze', null, zh)).toBe('');
    expect(noteKindLabelKey('cloze', catalog)).toBe('notes.kind.cloze');
    expect(noteKindLabelKey('nope', catalog)).toBeNull();
    expect(noteKindLabelKey(null, catalog)).toBeNull();
    expect(noteKindLabelKey('cloze', null)).toBeNull();
  });

  it('renders the default preset through the catalog instead of its storage name', () => {
    // 库内名 "Default"（store.DefaultPresetName）是默认预设的身份标识：直接印在中文界面上
    // 就是英文。判据是服务端的 is_default，用户自建的预设照原样显示名字。
    expect(presetDisplayName('Default', true, zh)).toBe('默认');
    expect(presetDisplayName('Default', true, en)).toBe('Default');
    expect(presetDisplayName('Evening', false, zh)).toBe('Evening');
    expect(presetDisplayName('Evening', false, en)).toBe('Evening');
  });

  it('builds the create-deck preset options from the preset list alone', () => {
    // 回归：下拉曾硬编码一个「默认预设」哨兵项再加真实列表，只有一条预设时会出现两条
    // 「默认」（哨兵文案与 presets.default_name 几乎同字），看着像一条没删掉的预设。
    // 选项必须与接口返回的预设一一对应，不多不少。
    const one = presetSelectOptions([{ id: '6', name: 'Default', is_default: true }], zh);
    expect(one.options).toEqual([{ value: '6', label: '默认' }]);
    expect(one.selected).toBe('6');

    const many = presetSelectOptions(
      [
        { id: '7', name: 'Evening', is_default: false },
        { id: '6', name: 'Default', is_default: true },
      ],
      en
    );
    expect(many.options).toEqual([
      { value: '7', label: 'Evening' },
      { value: '6', label: 'Default' },
    ]);
    expect(many.selected).toBe('6');
  });

  it('preselects the first preset when none is flagged default, and nothing when the list is empty', () => {
    // 没有 is_default 时退回第一条；列表为空（拉取失败）时 selected 为空串，
    // 调用方据此按服务端默认预设提交（preset_id 为空串）。
    expect(presetSelectOptions([{ id: '9', name: 'Morning', is_default: false }], zh).selected).toBe('9');
    const empty = presetSelectOptions([], zh);
    expect(empty.options).toEqual([]);
    expect(empty.selected).toBe('');
  });
});
