import { describe, it, expect } from 'vitest';
import { arrayToLines, emptyFields, linesToArray, type FieldSpec } from '../lib/card-fields';

/**
 * The edit form's field model: every note type field is an HTML string, while array and boolean controls need a
 * usable blank value and the server's boolean default, so "never touched the switch, saved once" does not flip it.
 */
describe('field model initialization', () => {
  it('starts array and boolean fields at a usable empty value', () => {
    const specs: FieldSpec[] = [
      { key: 'question', control: 'textarea', required: true },
      { key: 'options', control: 'lines', required: true },
      { key: 'answers', control: 'indexes', required: true },
      { key: 'ordered', control: 'bool', required: false },
      { key: 'ignore_case', control: 'bool', required: false, default: true },
      { key: 'ignore_whitespace', control: 'bool', required: false, default: true },
      { key: 'value', control: 'number', required: false },
    ];
    const fields = emptyFields(specs);
    expect(fields.options).toEqual([]);
    expect(fields.answers).toEqual([]);
    expect(fields.ordered).toBe(false);
    // The server default is true (ignore case/whitespace), so the form default must match.
    expect(fields.ignore_case).toBe(true);
    expect(fields.ignore_whitespace).toBe(true);
    expect(fields.value).toBe('');
  });

  it('converts a lines field to and from one item per line', () => {
    expect(linesToArray('red\ngreen\nblue')).toEqual(['red', 'green', 'blue']);
    expect(arrayToLines(['red', 'green'])).toBe('red\ngreen');
    expect(arrayToLines(undefined)).toBe('');
  });
});
