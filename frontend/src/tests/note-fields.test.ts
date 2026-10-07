import { describe, it, expect } from 'vitest';
import { readFileSync } from 'node:fs';
import { fileURLToPath } from 'node:url';
import {
  CARD_KIND_FIELDS,
  emptyFields,
  fieldsForKind,
  toFormFields,
  toPayloadFields,
} from '../lib/card-fields';

describe('card field schema mirrors the backend registry', () => {
  // 这份清单是 internal/cardtype/builtin.go 注册的题型与各自 Validate 收的字段名。
  // 字段名写错时服务端会拒绝保存，而界面不会报错——所以在这里钉死。
  const expected: Record<string, string[]> = {
    basic: ['front', 'back'],
    basic_both: ['front', 'back'],
    cloze: ['text'],
    list: ['prompt', 'items', 'ordered'],
    typed: ['prompt', 'answer', 'accept', 'ignore_case', 'ignore_whitespace'],
    numeric: ['prompt', 'value', 'unit', 'tolerance_absolute', 'tolerance_relative'],
    choice_single: ['question', 'options', 'answer'],
    choice_multi: ['question', 'options', 'answers'],
    true_false: ['statement', 'answer'],
    short_answer: ['prompt', 'reference'],
  };

  it('covers exactly the registered card types', () => {
    expect(Object.keys(CARD_KIND_FIELDS).sort()).toEqual(Object.keys(expected).sort());
  });

  for (const [kind, keys] of Object.entries(expected)) {
    it(`${kind} declares the backend field names in order`, () => {
      expect(CARD_KIND_FIELDS[kind]?.map((spec) => spec.key)).toEqual(keys);
    });
  }

  it('exposes the shared optional fields for every type', () => {
    for (const kind of Object.keys(expected)) {
      const keys = fieldsForKind(kind).map((spec) => spec.key);
      expect(keys, kind).toContain('source_url');
      expect(keys, kind).toContain('extra');
    }
  });
});

describe('field model conversions', () => {
  it('starts array and boolean fields at a usable empty value', () => {
    const fields = emptyFields('choice_multi');
    expect(fields.options).toEqual([]);
    expect(fields.answers).toEqual([]);
    expect(emptyFields('list').ordered).toBe(false);
    // 服务端默认是 true（忽略大小写/空白），表单默认值必须一致，
    // 否则「没动过开关、保存一次」会把行为改成 false。
    expect(emptyFields('typed').ignore_case).toBe(true);
    expect(emptyFields('typed').ignore_whitespace).toBe(true);
  });

  it('folds server fields into the form model and fills the missing keys', () => {
    const form = toFormFields('basic', { front: 'Q' });
    expect(form.front).toBe('Q');
    expect(form.back).toBe('');
  });

  it('drops empty values on submit but keeps an explicit false', () => {
    const payload = toPayloadFields({
      prompt: '  ',
      answer: 'A',
      accept: ['  ', 'B'],
      items: [],
      ignore_case: false,
      ordered: true,
    });
    // 空串/空数组一律省略：服务端的可选字段校验会把它们当非法值拒绝。
    expect(payload).toEqual({ answer: 'A', accept: ['B'], ignore_case: false, ordered: true });
    expect('prompt' in payload).toBe(false);
    expect('items' in payload).toBe(false);
  });

  it('keeps unknown keys out of the form model so the view can preserve them separately', () => {
    const form = toFormFields('basic', { front: 'Q', back: 'A', custom: 'x' });
    expect(Object.keys(form)).not.toContain('custom');
  });
});

describe('the note editor no longer exposes JSON', () => {
  const view = readFileSync(
    fileURLToPath(new URL('../lib/views/NoteEditView.svelte', import.meta.url)),
    'utf8'
  );

  it('renders the per-type field form instead of a JSON textarea', () => {
    expect(view).toContain('<NoteFieldsForm');
    expect(view).not.toContain('JSON.stringify');
    expect(view).not.toContain('note-fields-editor');
  });

  it('lets the user switch the card type', () => {
    expect(view).toContain('testId="note-edit-kind"');
    expect(view).toContain('onValueChange={changeKind}');
  });
});
