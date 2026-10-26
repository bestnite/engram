import { describe, expect, it } from 'vitest';
import {
  buildShortcode,
  insertSource,
  type ShortcodeBuild,
  type ShortcodeInput,
} from '../lib/shortcode-source';

/**
 * The source builder is the only place the editor decides which text is a shortcode. Every case
 * here mirrors a rule of the server parser in `internal/render/shortcode.go`: an accepted case must
 * produce exactly the source that parser expands, and a rejected case is one the parser would print
 * verbatim instead of expanding.
 */

const SHA = 'a1b2c3d4'.repeat(8);
const MEDIA = `/media/${SHA}`;

function rejected(result: ShortcodeBuild, field: string, code: string) {
  expect(result.ok).toBe(false);
  if (result.ok) return;
  expect({ field: result.issue.field, code: result.issue.code }).toEqual({ field, code });
}

describe('buildShortcode — what the server accepts', () => {
  const accepted: Array<{ name: string; input: ShortcodeInput; source: string }> = [
    {
      name: 'tts without parameters',
      input: { name: 'tts', body: 'こんにちは' },
      source: '{{tts:こんにちは}}',
    },
    {
      name: 'tts with language and rate',
      input: { name: 'tts', body: 'Hello', lang: 'ja-JP', rate: '0.9' },
      source: '{{tts:Hello|lang=ja-JP|rate=0.9}}',
    },
    {
      name: 'tts with only a rate',
      input: { name: 'tts', body: 'Hi', rate: '1.25' },
      source: '{{tts:Hi|rate=1.25}}',
    },
    {
      // A bare `|` is body text: only `|name=` starts the parameter list.
      name: 'tts body containing a bare pipe',
      input: { name: 'tts', body: 'a|b' },
      source: '{{tts:a|b}}',
    },
    {
      name: 'tts language and rate are trimmed',
      input: { name: 'tts', body: 'Hi', lang: ' ja-JP ', rate: ' 0.9 ' },
      source: '{{tts:Hi|lang=ja-JP|rate=0.9}}',
    },
    {
      name: 'tts with the hide flag alone',
      input: { name: 'tts', body: 'Hi', hide: true },
      source: '{{tts:Hi|hide}}',
    },
    {
      name: 'tts hide is written after every parameter',
      input: { name: 'tts', body: 'Hello', lang: 'ja-JP', rate: '0.9', hide: true },
      source: '{{tts:Hello|lang=ja-JP|rate=0.9|hide}}',
    },
    {
      name: 'tts with the hide flag unset keeps the plain source',
      input: { name: 'tts', body: 'Hi', lang: 'ja-JP', hide: false },
      source: '{{tts:Hi|lang=ja-JP}}',
    },
    {
      name: 'audio source alone',
      input: { name: 'audio', source: MEDIA },
      source: `{{audio:${MEDIA}}}`,
    },
    {
      name: 'audio with every parameter',
      input: { name: 'audio', source: MEDIA, start: '12.5', end: '15', rate: '0.75', loop: true },
      source: `{{audio:${MEDIA}|start=12.5|end=15|rate=0.75|loop}}`,
    },
    {
      name: 'audio keeps the declared parameter order',
      input: { name: 'audio', source: MEDIA, rate: '1', start: '0', loop: true },
      source: `{{audio:${MEDIA}|start=0|rate=1|loop}}`,
    },
    {
      name: 'audio with a start only',
      input: { name: 'audio', source: ` ${MEDIA} `, start: '3' },
      source: `{{audio:${MEDIA}|start=3}}`,
    },
    {
      name: 'ruby with two parts',
      input: { name: 'ruby', base: '漢字', reading: 'かんじ' },
      source: '{{ruby:漢字|かんじ}}',
    },
    {
      name: 'ruby keeps the parts as typed',
      input: { name: 'ruby', base: '漢 字', reading: 'かん じ' },
      source: '{{ruby:漢 字|かん じ}}',
    },
  ];

  for (const item of accepted) {
    it(item.name, () => {
      expect(buildShortcode(item.input)).toEqual({ ok: true, source: item.source });
    });
  }
});

describe('buildShortcode — what the server would print verbatim', () => {
  const rejectedCases: Array<{ name: string; input: ShortcodeInput; field: string; code: string }> = [
    {
      name: 'tts body empty',
      input: { name: 'tts', body: '' },
      field: 'body',
      code: 'body_required',
    },
    {
      name: 'tts body with a newline',
      input: { name: 'tts', body: 'a\nb' },
      field: 'body',
      code: 'body_single_line',
    },
    {
      name: 'tts body opening a nested shortcode',
      input: { name: 'tts', body: 'a{{b' },
      field: 'body',
      code: 'body_nested',
    },
    {
      name: 'tts body containing the body terminator',
      input: { name: 'tts', body: 'a}}b' },
      field: 'body',
      code: 'body_closing',
    },
    {
      name: 'tts body starting a parameter list',
      input: { name: 'tts', body: 'a|note=b' },
      field: 'body',
      code: 'body_separator',
    },
    {
      // `hide` is a bare flag: written as `hide=1` the server prints the whole shortcode verbatim.
      name: 'tts body carrying hide as a valued parameter',
      input: { name: 'tts', body: 'a|hide=1' },
      field: 'body',
      code: 'body_separator',
    },
    {
      name: 'tts body with a control character',
      input: { name: 'tts', body: 'a\u0001b' },
      field: 'body',
      code: 'body_control',
    },
    {
      name: 'tts language with an underscore',
      input: { name: 'tts', body: 'Hi', lang: 'ja_JP' },
      field: 'lang',
      code: 'lang_shape',
    },
    {
      name: 'tts language with a one-letter primary subtag',
      input: { name: 'tts', body: 'Hi', lang: 'j' },
      field: 'lang',
      code: 'lang_shape',
    },
    {
      name: 'tts language with too long a subtag',
      input: { name: 'tts', body: 'Hi', lang: 'ja-JPANPANPAN' },
      field: 'lang',
      code: 'lang_shape',
    },
    {
      name: 'tts rate with a trailing dot',
      input: { name: 'tts', body: 'Hi', rate: '1.' },
      field: 'rate',
      code: 'rate_shape',
    },
    {
      name: 'tts rate negative',
      input: { name: 'tts', body: 'Hi', rate: '-1' },
      field: 'rate',
      code: 'rate_shape',
    },
    {
      name: 'tts rate with seven decimals',
      input: { name: 'tts', body: 'Hi', rate: '1.1234567' },
      field: 'rate',
      code: 'rate_shape',
    },
    {
      name: 'audio source empty',
      input: { name: 'audio', source: '' },
      field: 'source',
      code: 'media_required',
    },
    {
      name: 'audio source that is not a media path',
      input: { name: 'audio', source: 'https://example.com/sound.mp3' },
      field: 'source',
      code: 'media_shape',
    },
    {
      name: 'audio source with a short hash',
      input: { name: 'audio', source: '/media/abc' },
      field: 'source',
      code: 'media_shape',
    },
    {
      name: 'audio source with an uppercase hash',
      input: { name: 'audio', source: `/media/${SHA.toUpperCase()}` },
      field: 'source',
      code: 'media_shape',
    },
    {
      name: 'audio start not a number',
      input: { name: 'audio', source: MEDIA, start: 'abc' },
      field: 'start',
      code: 'time_shape',
    },
    {
      name: 'audio end with seven decimals',
      input: { name: 'audio', source: MEDIA, end: '1.1234567' },
      field: 'end',
      code: 'time_shape',
    },
    {
      name: 'audio rate with a comma',
      input: { name: 'audio', source: MEDIA, rate: '0,9' },
      field: 'rate',
      code: 'rate_shape',
    },
    {
      // Deliberate client rule: the server accepts it, but a segment whose start is past its end never plays.
      name: 'audio range reversed',
      input: { name: 'audio', source: MEDIA, start: '15', end: '12.5' },
      field: 'end',
      code: 'range_order',
    },
    {
      name: 'audio range empty',
      input: { name: 'audio', source: MEDIA, start: '15', end: '15' },
      field: 'end',
      code: 'range_order',
    },
    {
      name: 'ruby base empty',
      input: { name: 'ruby', base: '', reading: 'かんじ' },
      field: 'base',
      code: 'ruby_base_required',
    },
    {
      name: 'ruby reading empty',
      input: { name: 'ruby', base: '漢字', reading: '' },
      field: 'reading',
      code: 'ruby_reading_required',
    },
    {
      name: 'ruby base containing an equals sign',
      input: { name: 'ruby', base: 'a=b', reading: 'c' },
      field: 'base',
      code: 'ruby_part',
    },
    {
      name: 'ruby reading containing a pipe',
      input: { name: 'ruby', base: 'a', reading: 'b|c' },
      field: 'reading',
      code: 'ruby_part',
    },
    {
      name: 'ruby base opening a nested shortcode',
      input: { name: 'ruby', base: 'a{{b', reading: 'c' },
      field: 'base',
      code: 'ruby_part',
    },
    {
      name: 'ruby reading with a newline',
      input: { name: 'ruby', base: 'a', reading: 'b\nc' },
      field: 'reading',
      code: 'ruby_part',
    },
  ];

  for (const item of rejectedCases) {
    it(item.name, () => {
      rejected(buildShortcode(item.input), item.field, item.code);
    });
  }
});

describe('buildShortcode — the hide flag belongs to tts', () => {
  // The panel cannot construct these shapes, but the builder must never turn a stray flag into source the server
  // prints verbatim: `hide` on audio or ruby is not part of their grammar.
  it('writes no hide flag for audio', () => {
    const input = { name: 'audio', source: MEDIA, start: '2', loop: true, hide: true } as unknown as ShortcodeInput;
    expect(buildShortcode(input)).toEqual({ ok: true, source: `{{audio:${MEDIA}|start=2|loop}}` });
  });

  it('writes no hide flag for ruby', () => {
    const input = { name: 'ruby', base: '漢字', reading: 'かんじ', hide: true } as unknown as ShortcodeInput;
    expect(buildShortcode(input)).toEqual({ ok: true, source: '{{ruby:漢字|かんじ}}' });
  });
});

describe('insertSource', () => {
  const cases: Array<{
    name: string;
    text: string;
    source: string;
    caret?: { start: number; end: number } | null;
    text_after: string;
    caret_after: { start: number; end: number };
  }> = [
    {
      name: 'writes into an empty field without a leading line break',
      text: '',
      source: 'X',
      caret: null,
      text_after: 'X',
      caret_after: { start: 1, end: 1 },
    },
    {
      name: 'appends on a new line when the field has content',
      text: 'abc',
      source: 'X',
      caret: null,
      text_after: 'abc\nX',
      caret_after: { start: 5, end: 5 },
    },
    {
      name: 'appends without a second line break when the field already ends with one',
      text: 'abc\n',
      source: 'X',
      caret: null,
      text_after: 'abc\nX',
      caret_after: { start: 5, end: 5 },
    },
    {
      name: 'splices at a collapsed caret',
      text: 'abc',
      source: 'X',
      caret: { start: 1, end: 1 },
      text_after: 'aXbc',
      caret_after: { start: 2, end: 2 },
    },
    {
      name: 'splices at the start',
      text: 'abc',
      source: 'X',
      caret: { start: 0, end: 0 },
      text_after: 'Xabc',
      caret_after: { start: 1, end: 1 },
    },
    {
      name: 'replaces a selection',
      text: 'abc',
      source: 'X',
      caret: { start: 1, end: 3 },
      text_after: 'aX',
      caret_after: { start: 2, end: 2 },
    },
    {
      name: 'clamps a caret left over from a longer text',
      text: 'ab',
      source: 'X',
      caret: { start: 10, end: 12 },
      text_after: 'abX',
      caret_after: { start: 3, end: 3 },
    },
    {
      name: 'clamps a reversed selection to a collapsed caret',
      text: 'abc',
      source: 'X',
      caret: { start: 2, end: 0 },
      text_after: 'abXc',
      caret_after: { start: 3, end: 3 },
    },
  ];

  for (const item of cases) {
    it(item.name, () => {
      expect(insertSource(item.text, item.source, item.caret)).toEqual({
        text: item.text_after,
        caret: item.caret_after,
      });
    });
  }
});
