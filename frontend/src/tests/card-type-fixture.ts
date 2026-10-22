import type { CardTypeDescription, CardTypeField, CardTypesResponse } from '../lib/api/types';
import { createCatalog, setCardTypes } from '../lib/card-types';

/**
 * Sample response of GET /api/v1/card-types, aligned word for word with the authoritative implementation in internal/cardtype:
 * each card type's fields (including the generic optional fields source_url / extra appended by the server), the front and back fields,
 * the answer control and the grading markers. Tests use it in place of a real server to pin how the frontend consumes the contract.
 *
 * kinds are in kind lexicographic order (the result of sort.Strings on the server side), used to assert "stable order".
 */

/** Optional fields shared by every card type; the server has appended them at the end of each card type's field list. */
function common(): CardTypeField[] {
  return [
    { key: 'source_url', control: 'text', required: false },
    { key: 'extra', control: 'textarea', required: false },
  ];
}

function withCommon(own: CardTypeField[]): CardTypeField[] {
  return [...own, ...common()];
}

const kinds: CardTypeDescription[] = [
  {
    kind: 'basic',
    label_key: 'cardtype.basic',
    graded: false,
    answer_control: 'none',
    front_field: 'front',
    back_field: 'back',
    prompt_field: '',
    options_field: '',
    fields: withCommon([
      { key: 'front', control: 'textarea', required: true },
      { key: 'back', control: 'textarea', required: true },
    ]),
  },
  {
    kind: 'basic_both',
    label_key: 'cardtype.basic_both',
    graded: false,
    answer_control: 'none',
    front_field: 'front',
    back_field: 'back',
    prompt_field: '',
    options_field: '',
    fields: withCommon([
      { key: 'front', control: 'textarea', required: true },
      { key: 'back', control: 'textarea', required: true },
    ]),
  },
  {
    kind: 'choice_multi',
    label_key: 'cardtype.choice_multi',
    graded: true,
    answer_control: 'multi',
    front_field: 'question',
    back_field: 'options',
    prompt_field: 'question',
    options_field: 'options',
    fields: withCommon([
      { key: 'question', control: 'textarea', required: true },
      { key: 'options', control: 'lines', required: true },
      { key: 'answers', control: 'indexes', required: true },
    ]),
  },
  {
    kind: 'choice_single',
    label_key: 'cardtype.choice_single',
    graded: true,
    answer_control: 'single',
    front_field: 'question',
    back_field: 'options',
    prompt_field: 'question',
    options_field: 'options',
    fields: withCommon([
      { key: 'question', control: 'textarea', required: true },
      { key: 'options', control: 'lines', required: true },
      { key: 'answer', control: 'index', required: true },
    ]),
  },
  {
    kind: 'cloze',
    label_key: 'cardtype.cloze',
    graded: true,
    answer_control: 'blanks',
    front_field: 'text',
    back_field: 'text',
    prompt_field: '',
    options_field: '',
    fields: withCommon([{ key: 'text', control: 'textarea', required: true }]),
  },
  {
    kind: 'list',
    label_key: 'cardtype.list',
    graded: true,
    answer_control: 'items',
    front_field: 'prompt',
    back_field: 'items',
    prompt_field: 'prompt',
    options_field: '',
    fields: withCommon([
      { key: 'prompt', control: 'textarea', required: true },
      { key: 'items', control: 'lines', required: true },
      { key: 'ordered', control: 'bool', required: false, default: false },
    ]),
  },
  {
    kind: 'numeric',
    label_key: 'cardtype.numeric',
    graded: true,
    answer_control: 'number',
    front_field: 'prompt',
    back_field: 'value',
    prompt_field: 'prompt',
    options_field: '',
    fields: withCommon([
      { key: 'prompt', control: 'textarea', required: true },
      { key: 'value', control: 'number', required: true },
      { key: 'unit', control: 'text', required: false },
      { key: 'tolerance_absolute', control: 'number', required: false },
      { key: 'tolerance_relative', control: 'number', required: false },
    ]),
  },
  {
    kind: 'short_answer',
    label_key: 'cardtype.short_answer',
    graded: false,
    answer_control: 'essay',
    front_field: 'prompt',
    back_field: 'reference',
    prompt_field: 'prompt',
    options_field: '',
    fields: withCommon([
      { key: 'prompt', control: 'textarea', required: true },
      { key: 'reference', control: 'textarea', required: false },
    ]),
  },
  {
    kind: 'true_false',
    label_key: 'cardtype.true_false',
    graded: true,
    answer_control: 'bool',
    front_field: 'statement',
    back_field: 'answer',
    prompt_field: 'statement',
    options_field: '',
    fields: withCommon([
      { key: 'statement', control: 'textarea', required: true },
      { key: 'answer', control: 'bool', required: true },
    ]),
  },
  {
    kind: 'typed',
    label_key: 'cardtype.typed',
    graded: true,
    answer_control: 'text',
    front_field: 'prompt',
    back_field: 'answer',
    prompt_field: 'prompt',
    options_field: '',
    fields: withCommon([
      { key: 'prompt', control: 'textarea', required: true },
      { key: 'answer', control: 'text', required: true },
      { key: 'accept', control: 'lines', required: false },
      // The server ignores case and whitespace differences by default: the form defaults must match, otherwise a single save flips the behavior to false.
      { key: 'ignore_case', control: 'bool', required: false, default: true },
      { key: 'ignore_whitespace', control: 'bool', required: false, default: true },
    ]),
  },
];

/** Sample response body. */
export const CARD_TYPES: CardTypesResponse = { kinds };

/** Inject the sample catalogue into the metadata store, for the cases that render views. */
export function seedCardTypes(): void {
  setCardTypes(createCatalog(kinds));
}

/** Clear the metadata store (simulating not yet loaded / fetch failure). */
export function clearCardTypes(): void {
  setCardTypes(null);
}
