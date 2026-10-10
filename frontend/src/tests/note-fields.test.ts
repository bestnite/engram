import { describe, it, expect } from 'vitest';
import { emptyFields, toFormFields, toPayloadFields } from '../lib/card-fields';
import {
  answerControl,
  backField,
  createCatalog,
  fieldsForKind,
  frontField,
  isGraded,
  kindOrder,
  optionsField,
  promptField,
} from '../lib/card-types';
import { CARD_TYPES } from './card-type-fixture';

/**
 * 字段表不再由前端硬编码：它随题型自描述（GET /api/v1/card-types）到达。
 * 这些用例以样例响应为输入，钉住前端「消费服务端字段表」的行为——字段名写错时服务端
 * 会拒绝保存而界面不会报错，所以在这里核对每一处投影。
 */
const catalog = createCatalog(CARD_TYPES.kinds);

/** 各题型自有字段（不含服务端追加的通用可选字段）。 */
const OWN_FIELDS: Record<string, string[]> = {
  basic: ['front', 'back'],
  basic_both: ['front', 'back'],
  choice_multi: ['question', 'options', 'answers'],
  choice_single: ['question', 'options', 'answer'],
  cloze: ['text'],
  list: ['prompt', 'items', 'ordered'],
  numeric: ['prompt', 'value', 'unit', 'tolerance_absolute', 'tolerance_relative'],
  short_answer: ['prompt', 'reference'],
  true_false: ['statement', 'answer'],
  typed: ['prompt', 'answer', 'accept', 'ignore_case', 'ignore_whitespace'],
};

describe('the catalog is driven by the server self-description', () => {
  it('keeps the server order and exposes every registered kind', () => {
    expect(kindOrder(catalog)).toEqual([
      'basic',
      'basic_both',
      'choice_multi',
      'choice_single',
      'cloze',
      'list',
      'numeric',
      'short_answer',
      'true_false',
      'typed',
    ]);
  });

  it('projects the server field names in order, followed by the shared optional fields', () => {
    for (const [kind, own] of Object.entries(OWN_FIELDS)) {
      const keys = fieldsForKind(catalog, kind).map((spec) => spec.key);
      expect(keys, kind).toEqual([...own, 'source_url', 'extra']);
    }
  });

  it('never appends the shared optional fields twice', () => {
    for (const kind of kindOrder(catalog)) {
      const keys = fieldsForKind(catalog, kind).map((spec) => spec.key);
      expect(keys.filter((key) => key === 'source_url').length, kind).toBe(1);
      expect(keys.filter((key) => key === 'extra').length, kind).toBe(1);
    }
  });

  it('returns an empty field table for an unknown kind instead of guessing', () => {
    expect(fieldsForKind(catalog, 'made_up_kind')).toEqual([]);
    expect(fieldsForKind(null, 'basic')).toEqual([]);
  });

  it('reads the front/back/prompt/options fields from the server mapping', () => {
    expect(frontField(catalog, 'short_answer')).toBe('prompt');
    expect(backField(catalog, 'short_answer')).toBe('reference');
    expect(frontField(catalog, 'list')).toBe('prompt');
    expect(backField(catalog, 'list')).toBe('items');
    expect(promptField(catalog, 'true_false')).toBe('statement');
    expect(optionsField(catalog, 'choice_multi')).toBe('options');
    expect(optionsField(catalog, 'typed')).toBe('');
  });

  it('classifies graded kinds from the server flag, not a local list', () => {
    for (const kind of ['typed', 'numeric', 'choice_single', 'choice_multi', 'true_false', 'cloze', 'list']) {
      expect(isGraded(catalog, kind), kind).toBe(true);
    }
    for (const kind of ['basic', 'basic_both', 'short_answer']) {
      expect(isGraded(catalog, kind), kind).toBe(false);
    }
    expect(isGraded(catalog, 'made_up_kind')).toBe(false);
  });

  it('maps the server answer control for the review input', () => {
    expect(answerControl(catalog, 'typed')).toBe('text');
    expect(answerControl(catalog, 'numeric')).toBe('number');
    expect(answerControl(catalog, 'choice_single')).toBe('single');
    expect(answerControl(catalog, 'choice_multi')).toBe('multi');
    expect(answerControl(catalog, 'true_false')).toBe('bool');
    expect(answerControl(catalog, 'short_answer')).toBe('none');
    expect(answerControl(catalog, 'made_up_kind')).toBe('none');
  });
});

describe('field model conversions', () => {
  it('starts array and boolean fields at a usable empty value', () => {
    const fields = emptyFields(fieldsForKind(catalog, 'choice_multi'));
    expect(fields.options).toEqual([]);
    expect(fields.answers).toEqual([]);
    expect(emptyFields(fieldsForKind(catalog, 'list')).ordered).toBe(false);
    // 服务端默认是 true（忽略大小写/空白），表单默认值必须一致，
    // 否则「没动过开关、保存一次」会把行为改成 false。
    expect(emptyFields(fieldsForKind(catalog, 'typed')).ignore_case).toBe(true);
    expect(emptyFields(fieldsForKind(catalog, 'typed')).ignore_whitespace).toBe(true);
  });

  it('folds server fields into the form model and fills the missing keys', () => {
    const form = toFormFields(fieldsForKind(catalog, 'basic'), { front: 'Q' });
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

  it('keeps numeric fields as numbers when folding server fields into the form', () => {
    const form = toFormFields(fieldsForKind(catalog, 'numeric'), { prompt: 'g', value: 9.81, tolerance_absolute: 0 });
    expect(form.value).toBe(9.81);
    // 0 是合法取值，不能当成「未填」。
    expect(form.tolerance_absolute).toBe(0);
    expect(form.tolerance_relative).toBe('');
    expect(toPayloadFields(form)).toMatchObject({ value: 9.81, tolerance_absolute: 0 });
  });

  it('parses numeric strings and treats unparsable values as unset', () => {
    const form = toFormFields(fieldsForKind(catalog, 'numeric'), { value: '42', tolerance_absolute: 'abc' });
    expect(form.value).toBe(42);
    expect(form.tolerance_absolute).toBe('');
  });

  it('keeps unknown keys out of the form model so the view can preserve them separately', () => {
    const form = toFormFields(fieldsForKind(catalog, 'basic'), { front: 'Q', back: 'A', custom: 'x' });
    expect(Object.keys(form)).not.toContain('custom');
  });
});
