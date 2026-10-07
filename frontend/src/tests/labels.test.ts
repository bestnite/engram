import { describe, it, expect } from 'vitest';
import { formatMessage } from '../lib/i18n';
import {
  deckVisibilityLabel,
  deckVisibilityLabelKey,
  noteKindLabel,
  noteKindLabelKey,
} from '../lib/labels';

/**
 * 枚举文案映射：后端枚举值一律经 lib/labels.ts 翻成语言包文案，未知值渲染成空。
 *
 * 判据不是「有没有引号」，而是「这个串是给机器读还是给人读的」——模板里直接插原值
 * （`{deck.visibility}`）在中文界面里会印出 `private`，与硬编码英文同害，却不容易看出来。
 */

const zh = (key: string) => formatMessage('zh-CN', key);
const en = (key: string) => formatMessage('en', key);

describe('enum labels go through the catalog', () => {
  it('translates deck visibility instead of printing the enum', () => {
    expect(deckVisibilityLabel('private', zh)).toBe('私有');
    expect(deckVisibilityLabel('unlisted', zh)).toBe('不公开');
    expect(deckVisibilityLabel('public', zh)).toBe('公开');
    expect(deckVisibilityLabel('public', en)).toBe('Public');
  });

  it('translates card kinds instead of printing the enum', () => {
    expect(noteKindLabel('basic_both', zh)).toBe('双向问答');
    expect(noteKindLabel('cloze', zh)).toBe('填空');
    expect(noteKindLabel('basic', en)).toBe('Basic');
  });

  it('renders nothing for an unknown or missing value', () => {
    // 关键：未知值绝不能回落到键名或机器词。$t 找不到键时会原样返回键名，
    // 直接插原值会印英文——两者都比「少一格徽标」糟。
    expect(deckVisibilityLabel('secret', zh)).toBe('');
    expect(deckVisibilityLabel('', zh)).toBe('');
    expect(deckVisibilityLabel(null, zh)).toBe('');
    expect(deckVisibilityLabel(undefined, zh)).toBe('');
    expect(noteKindLabel('made_up_kind', zh)).toBe('');
    expect(noteKindLabel(null, zh)).toBe('');
    expect(deckVisibilityLabelKey('private')).toBe('deck.sharing.visibility.private');
    expect(deckVisibilityLabelKey('nope')).toBeNull();
    expect(noteKindLabelKey('cloze')).toBe('notes.kind.cloze');
    expect(noteKindLabelKey('nope')).toBeNull();
  });
});
