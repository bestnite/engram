import { describe, it, expect } from 'vitest';
import { rewriteFieldReferences } from '../lib/note-type-template';

describe('field rename rewrites template references', () => {
  it('rewrites the plain, conditional and filter forms', () => {
    const template = [
      '{{Old}}',
      '{{#Old}}shown when filled{{/Old}}',
      '{{^Old}}shown when empty{{/Old}}',
      '{{text:Old}}',
      '{{type:Old}}',
      '{{hint:Old}}',
      '{{furigana:Old}}',
      '{{tts en_US:Old}}',
    ].join(' ');
    const got = rewriteFieldReferences(template, 'Old', 'New');
    expect(got).toContain('{{New}}');
    expect(got).toContain('{{#New}}shown when filled{{/New}}');
    expect(got).toContain('{{^New}}shown when empty{{/New}}');
    expect(got).toContain('{{text:New}}');
    expect(got).toContain('{{type:New}}');
    expect(got).toContain('{{hint:New}}');
    expect(got).toContain('{{furigana:New}}');
    // The filter's language argument is not the field name and must survive unchanged.
    expect(got).toContain('{{tts en_US:New}}');
    expect(got).not.toContain('Old');
  });

  it('does not rewrite a name that only contains the old name or a filter with no field', () => {
    const template = '{{Older}} {{tts-voices:}} {{tts en_US:Other}}';
    expect(rewriteFieldReferences(template, 'Old', 'New')).toBe(template);
  });

  it('keeps surrounding whitespace inside the braces', () => {
    expect(rewriteFieldReferences('{{ Old }}', 'Old', 'New')).toBe('{{ New }}');
  });

  // A field name may contain `$`, and a replacement string would interpret `$&`, `` $` ``, `$'` and
  // `$$` as substitution patterns instead of writing them literally.
  const dollarNewNames = ['A$&B', 'A$`B', "A$'B", 'A$$B'];

  it.each(dollarNewNames)('writes %s verbatim in every reference form', (newName) => {
    const cases: Array<[string, string]> = [
      ['{{Old}}', `{{${newName}}}`],
      ['{{#Old}}', `{{#${newName}}}`],
      ['{{/Old}}', `{{/${newName}}}`],
      ['{{text:Old}}', `{{text:${newName}}}`],
    ];
    for (const [from, to] of cases) {
      expect(rewriteFieldReferences(from, 'Old', newName)).toBe(to);
      // The same form must survive inside larger template text.
      expect(rewriteFieldReferences(`before ${from} after`, 'Old', newName)).toBe(
        `before ${to} after`,
      );
    }
  });
});
