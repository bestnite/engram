import { describe, it, expect } from 'vitest';
import { readFileSync } from 'node:fs';
import { fileURLToPath } from 'node:url';
import { formatMessage } from '../lib/i18n';
import {
  deckVisibilityLabel,
  deckVisibilityLabelKey,
  noteKindLabel,
  noteKindLabelKey,
} from '../lib/labels';

/**
 * 枚举文案的守卫（DESIGN.md §8）。
 *
 * 缺陷形态：模板里直接插后端枚举值——`<span>{deck.visibility}</span>` 在中文界面里印
 * `private`，与硬编码英文结果一样，但**看起来不像**硬编码，所以「模板里不得出现用户可见
 * 字面量」那类扫描抓不到它。判据不是「有没有引号」，而是「这个串是给机器读还是给人读的」。
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

describe('no view prints a raw enum', () => {
  const read = (rel: string) => readFileSync(fileURLToPath(new URL(rel, import.meta.url)), 'utf8');

  // 五个曾经直接插原值的徽标（/、/decks、卡组详情标题、note 行、复习卡）。
  const cases = [
    ['../lib/views/HomeView.svelte', 'visibilityLabel(deck.visibility, $t)'],
    ['../lib/views/DecksView.svelte', 'visibilityLabel(deck.visibility, $t)'],
    ['../lib/views/DeckDetailView.svelte', 'visibilityLabel(deck?.visibility, $t)'],
    ['../lib/views/DeckDetailView.svelte', 'kindLabel(note.kind, $t)'],
    ['../lib/views/ReviewView.svelte', 'kindLabel(current.kind, $t)'],
  ] as const;

  for (const [rel, expected] of cases) {
    it(`${rel} renders ${expected}`, () => {
      const source = read(rel);
      expect(source).toContain(expected);
      // 反面：原值不得出现在模板插值位置上（`{deck.visibility}` 这类）。
      // helper 调用里当然含 `deck.visibility` 这几个字，所以查的是**带花括号的插值**形态。
      for (const raw of ['{deck.visibility}', '{deck?.visibility}', '{note.kind}', '{current.kind}']) {
        expect(source).not.toContain(raw);
      }
    });
  }

  it('the kind list comes from the field table, not a second copy', () => {
    const source = read('../lib/views/DeckDetailView.svelte');
    expect(source).toContain('const CARD_KINDS = Object.keys(CARD_KIND_FIELDS)');
  });
});
