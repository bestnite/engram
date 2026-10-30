import { describe, it, expect } from 'vitest';
import { renderPreview, rewriteFieldReferences } from '../lib/note-type-template';

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
});

describe('live template preview', () => {
  it('fills field references with the provided values', () => {
    const html = renderPreview('<div>{{Front}}</div><div>{{text:Back}}</div>', {
      Front: 'Front',
      Back: 'Back',
    });
    expect(html).toBe('<div>Front</div><div>Back</div>');
  });

  it('resolves conditionals and the FrontSide reference on the answer side', () => {
    const front = renderPreview('{{#Front}}{{Front}}{{/Front}}{{^Back}}fallback{{/Back}}', {
      Front: 'Question',
      Back: '',
    });
    expect(front).toBe('Questionfallback');
    const back = renderPreview('{{FrontSide}}<hr id=answer>{{Back}}', { Back: 'Answer' }, front);
    expect(back).toBe('Questionfallback<hr id=answer>Answer');
  });
});
