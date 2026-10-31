import { describe, expect, it } from 'vitest';
import { fieldText, formatBytes, formatDate, formatNumber, formatPercent, htmlToText } from '../lib/format';

describe('shared formatting', () => {
  it.each([
    [1234567, 'en', '1,234,567'],
    [0, 'zh-CN', '0'],
  ])('formatNumber(%s, %s)', (value, locale, want) => {
    expect(formatNumber(value, locale)).toBe(want);
  });

  it.each([
    [0.9482758620689655, 'en', '94.8%'],
    [1, 'zh-CN', '100.0%'],
    [0, 'en', '0.0%'],
  ])('formatPercent(%s, %s)', (rate, locale, want) => {
    expect(formatPercent(rate, locale)).toBe(want);
  });

  it.each([
    [0, '0 B'],
    [1023, '1,023 B'],
    [1536, '1.5 KiB'],
    [5 * 1024 * 1024, '5 MiB'],
    [-1, '0 B'],
  ])('formatBytes(%s)', (bytes, want) => {
    expect(formatBytes(bytes, 'en')).toBe(want);
  });

  it('formatDate formats a timestamp and passes an unparsable value through', () => {
    expect(formatDate('2026-10-02T12:00:00Z', 'en')).toBe('Oct 2, 2026');
    expect(formatDate('not a date', 'en')).toBe('not a date');
  });

  it.each([
    [undefined, ''],
    [null, ''],
    ['text', 'text'],
    [3, '3'],
    [true, 'true'],
    [['a', 'b'], 'a, b'],
    [[{ x: 1 }], '{"x":1}'],
    [{ x: 1 }, '{\n  "x": 1\n}'],
  ])('fieldText(%j)', (value, want) => {
    expect(fieldText(value)).toBe(want);
  });
});

describe('htmlToText', () => {
  it.each([
    ['paragraphs become one line', '<p>继续、延续。</p><p>第二段</p>', '继续、延续。 第二段'],
    ['a ruby reads as its base', '<p><ruby>赓<rt>gēng</rt></ruby>续</p>', '赓续'],
    ['ruby fallback parentheses are dropped', '<ruby>漢<rp>(</rp><rt>かん</rt><rp>)</rp></ruby>字', '漢字'],
    ['a speech tag is dropped after its visible text', 'bonjour[anki:tts lang=fr_FR]bonjour[/anki:tts]', 'bonjour'],
    ['a sound tag is dropped', 'listen [sound:a.mp3] now', 'listen now'],
    ['line breaks become spaces', 'a<br>b<br/>c', 'a b c'],
    ['inline markup is removed', '<b>bold</b> and <span style="color:red">red</span>', 'bold and red'],
    ['scripts and styles are dropped with their content', '<style>p{}</style>x<script>alert(1)</script>', 'x'],
    ['references are decoded', 'a &lt;b&gt; &amp; &quot;c&quot; &#39;d&#39; &#x4e2d;', 'a <b> & "c" \'d\' 中'],
    ['an unknown reference stays as written', '&copy; &#0;', '&copy; &#0;'],
    ['an image leaves nothing', '<img src="x.png">', ''],
    ['text without markup is unchanged', 'plain  text', 'plain text'],
  ])('%s', (_name, input, expected) => {
    expect(htmlToText(input)).toBe(expected);
  });
});
