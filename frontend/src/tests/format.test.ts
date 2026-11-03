import { describe, expect, it } from 'vitest';
import { fieldText, formatBytes, formatDate, formatDateTime, formatDuration, formatIsoDay, formatNumber, formatPercent, htmlToText } from '../lib/format';

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

  it('formatDuration picks the largest readable unit', () => {
    expect(formatDuration(250, 'en')).toBe('250 ms');
    expect(formatDuration(1500, 'en')).toBe('1.5 sec');
    expect(formatDuration(600_000, 'en', 'long')).toBe('10 minutes');
    expect(formatDuration(5_000, 'en', 'long')).toBe('5 seconds');
    expect(formatDuration(7_200_000, 'en', 'long')).toBe('2 hours');
    expect(formatDuration(-3, 'en')).toBe('0 ms');
  });

  it('formatDate formats a timestamp and passes an unparsable value through', () => {
    expect(formatDate('2026-10-02T12:00:00Z', 'en', 'UTC')).toBe('Oct 2, 2026');
    expect(formatDate('not a date', 'en', 'UTC')).toBe('not a date');
  });

  // The account's timezone decides the day, not the browser's; an empty or unknown name reads as UTC, as on the server.
  it.each([
    ['UTC', 'Oct 2, 2026'],
    ['Asia/Shanghai', 'Oct 3, 2026'],
    ['America/New_York', 'Oct 2, 2026'],
    ['', 'Oct 2, 2026'],
    ['Mars/Phobos', 'Oct 2, 2026'],
  ])('formatDate in %s', (tz, want) => {
    expect(formatDate('2026-10-02T20:00:00Z', 'en', tz)).toBe(want);
  });

  it.each([
    ['UTC', '2026-10-02'],
    ['Asia/Shanghai', '2026-10-03'],
    ['Mars/Phobos', '2026-10-02'],
  ])('formatIsoDay in %s', (tz, want) => {
    expect(formatIsoDay('2026-10-02T20:00:00Z', tz)).toBe(want);
    expect(formatIsoDay('not a date', tz)).toBe('');
  });

  it.each([
    ['UTC', 'Oct 2, 2026, 8:00 PM'],
    ['Asia/Shanghai', 'Oct 3, 2026, 4:00 AM'],
    ['Mars/Phobos', 'Oct 2, 2026, 8:00 PM'],
  ])('formatDateTime in %s', (tz, want) => {
    expect(formatDateTime('2026-10-02T20:00:00Z', 'en', tz).replace(/\u202f/g, ' ')).toBe(want);
    expect(formatDateTime('not a date', 'en', tz)).toBe('not a date');
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
