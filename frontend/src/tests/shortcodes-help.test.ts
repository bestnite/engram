import { describe, it, expect, beforeEach } from 'vitest';
import { render } from 'svelte/server';
import ShortcodesHelpView from '../lib/views/ShortcodesHelpView.svelte';
import { setLocale } from '../lib/i18n';
import { zhCN } from '../lib/i18n/locales/zh-CN';
import { en } from '../lib/i18n/locales/en';

/**
 * The shortcode reference page must teach the server's actual grammar. These cases pin the three names, the literal
 * examples, every rule and every parameter description in both catalogs, so a key that is added to the catalogs but
 * never rendered (or a page that prints a key instead of its text) fails here instead of shipping.
 */

// The examples are code: they are identical in both locales and must not be translated.
const TTS_EXAMPLE = '{{tts:こんにちは|lang=ja-JP|rate=0.9}}';
const RUBY_EXAMPLE = '{{ruby:漢字|かんじ}}';
// `<sha256>` is a placeholder in the example; Svelte escapes the opening angle bracket, so the rendered text
// carries `&lt;` and must never appear as real markup.
const AUDIO_EXAMPLE_ESCAPED = '{{audio:/media/&lt;sha256>|start=12.5|end=15|rate=0.75|loop}}';

/** Keys whose text the page shows directly; copy/copied/copy_failed only reach a button label or a toast. */
const RENDERED_KEYS = [
  'help.shortcode.title',
  'help.shortcode.intro',
  'help.shortcode.grammar.title',
  'help.shortcode.grammar.intro',
  'help.shortcode.grammar.line',
  'help.shortcode.grammar.names',
  'help.shortcode.grammar.params',
  'help.shortcode.example',
  'help.shortcode.param.optional',
  'help.shortcode.tts.title',
  'help.shortcode.tts.summary',
  'help.shortcode.tts.play',
  'help.shortcode.tts.lang',
  'help.shortcode.tts.rate',
  'help.shortcode.audio.title',
  'help.shortcode.audio.summary',
  'help.shortcode.audio.play',
  'help.shortcode.audio.src',
  'help.shortcode.audio.start',
  'help.shortcode.audio.end',
  'help.shortcode.audio.rate',
  'help.shortcode.audio.loop',
  'help.shortcode.ruby.title',
  'help.shortcode.ruby.summary',
  'help.shortcode.ruby.form',
  'help.shortcode.ruby.chars',
  'help.shortcode.rules.title',
  'help.shortcode.rule.fallback',
  'help.shortcode.rule.code',
  'help.shortcode.rule.text_kept',
  'help.shortcode.rule.options',
  'help.shortcode.rule.mail',
  'help.shortcode.rule.metadata',
  'help.shortcode.where.title',
  'help.shortcode.where.body',
] as const;

function renderIn(locale: 'zh-CN' | 'en'): string {
  setLocale(locale);
  return render(ShortcodesHelpView).html;
}

describe('ShortcodesHelpView', () => {
  beforeEach(() => {
    setLocale('zh-CN');
  });

  it('renders every documented key from the Chinese catalog', () => {
    const html = renderIn('zh-CN');
    for (const key of RENDERED_KEYS) {
      expect(html, `zh-CN ${key}`).toContain(zhCN[key]);
    }
    expect(html).toContain('复制 tts 示例');
  });

  it('renders every documented key from the English catalog', () => {
    const html = renderIn('en');
    for (const key of RENDERED_KEYS) {
      expect(html, `en ${key}`).toContain(en[key]);
    }
    expect(html).toContain('Copy the tts example');
  });

  it('names the three shortcodes and shows each example as literal text', () => {
    const html = renderIn('zh-CN');
    // The name chips next to the section heading; the heading itself is localized.
    expect(html).toContain('>tts</code>');
    expect(html).toContain('>audio</code>');
    expect(html).toContain('>ruby</code>');

    expect(html).toContain(TTS_EXAMPLE);
    expect(html).toContain(AUDIO_EXAMPLE_ESCAPED);
    expect(html).toContain(RUBY_EXAMPLE);
    // The generic form is literal too.
    expect(html).toContain('{{name:body|key=value|...}}');
    // The audio placeholder is text, not a tag: an unescaped `<sha256>` would be parsed as markup.
    expect(html).not.toContain('<sha256>');
  });

  it('keeps the rules about failures, code spans, mail and options visible', () => {
    for (const locale of ['zh-CN', 'en'] as const) {
      const html = renderIn(locale);
      const catalog = locale === 'zh-CN' ? zhCN : en;
      // The four rules that change what a user sees, not just how the syntax is written.
      expect(html).toContain(catalog['help.shortcode.rule.fallback']);
      expect(html).toContain(catalog['help.shortcode.rule.code']);
      expect(html).toContain(catalog['help.shortcode.rule.options']);
      // Mail renders ruby as base(reading), which must be spelled out for the reader.
      expect(html).toContain('漢字(かんじ)');
    }
  });

  it('prints no catalog key and no unresolved interpolation', () => {
    const html = renderIn('en');
    expect(html).not.toContain('help.shortcode.');
    expect(html).not.toContain('{$t(');
    expect(html).not.toContain('{name}');
    expect(html).not.toContain('undefined');
  });
});
