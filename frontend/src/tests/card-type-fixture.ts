import type { CardTypeDescription, CardTypeField, CardTypesResponse } from '../lib/api/types';
import { createCatalog, setCardTypes } from '../lib/card-types';

/**
 * GET /api/v1/card-types 的样例响应，逐字对齐 internal/cardtype 的权威实现：
 * 每个题型的字段（含服务端追加的通用可选字段 source_url / extra）、正反面字段、
 * 作答控件与判分标记。测试以它代替真实服务端，钉住前端消费契约的方式。
 *
 * kinds 按 kind 字典序（服务端 sort.Strings 的结果），供断言「顺序稳定」。
 */

/** 所有题型共用的可选字段，服务端已追加在每个题型字段末尾。 */
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
    graded: false,
    answer_control: 'none',
    front_field: 'prompt',
    back_field: 'items',
    prompt_field: '',
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
    answer_control: 'none',
    front_field: 'prompt',
    back_field: 'reference',
    prompt_field: '',
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
      // 服务端默认忽略大小写与空白差异：表单默认值必须一致，否则保存一次就把行为改成 false。
      { key: 'ignore_case', control: 'bool', required: false, default: true },
      { key: 'ignore_whitespace', control: 'bool', required: false, default: true },
    ]),
  },
];

/** 样例响应体。 */
export const CARD_TYPES: CardTypesResponse = { kinds };

/** 把样例目录注入元数据 store，供渲染视图的用例使用。 */
export function seedCardTypes(): void {
  setCardTypes(createCatalog(kinds));
}

/** 清空元数据 store（模拟尚未加载 / 拉取失败）。 */
export function clearCardTypes(): void {
  setCardTypes(null);
}
