import { describe, it, expect } from 'vitest';
import { emptyFields } from '../lib/card-fields';
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
 * The field table is no longer hardcoded in the frontend: it arrives with each card type's
 * self-description (GET /api/v1/card-types). These cases take sample responses as input and
 * pin how the frontend consumes the server's field table — a mistyped field name makes the
 * server reject the save while the UI stays quiet, so every projection is checked here.
 */
const catalog = createCatalog(CARD_TYPES.kinds);

/** Each card type's own fields (excluding the generic optional fields the server appends). */
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
    expect(answerControl(catalog, 'cloze')).toBe('blanks');
    expect(answerControl(catalog, 'list')).toBe('items');
    expect(answerControl(catalog, 'short_answer')).toBe('essay');
    expect(answerControl(catalog, 'basic')).toBe('none');
    expect(answerControl(catalog, 'made_up_kind')).toBe('none');
  });
});

describe('field model initialization', () => {
  it('starts array and boolean fields at a usable empty value', () => {
    const fields = emptyFields(fieldsForKind(catalog, 'choice_multi'));
    expect(fields.options).toEqual([]);
    expect(fields.answers).toEqual([]);
    expect(emptyFields(fieldsForKind(catalog, 'list')).ordered).toBe(false);
    // The server default is true (ignore case/whitespace), so the form default must match,
    // otherwise "never touched the switch, saved once" flips the behaviour to false.
    expect(emptyFields(fieldsForKind(catalog, 'typed')).ignore_case).toBe(true);
    expect(emptyFields(fieldsForKind(catalog, 'typed')).ignore_whitespace).toBe(true);
  });
});
