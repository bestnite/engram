/**
 * 题型 → 字段表（DESIGN.md §6.2）。
 *
 * 这是编辑页的单一数据源：字段顺序、控件形态、必填性都来自这里，视图不再按题型写十套
 * 表单，也不再让用户直接编辑 JSON。字段名必须与 internal/cardtype/*.go 的 Validate
 * 逐字一致——它是服务端的权威定义，这里只是它的界面投影。
 */
export type FieldControl =
  | 'text' // 单行文本
  | 'textarea' // 多行文本（Markdown / LaTeX）
  | 'lines' // 多行文本，每行一个数组元素
  | 'number' // 数值
  | 'bool' // 布尔
  | 'index' // 单选：取 options 的下标
  | 'indexes'; // 多选：取 options 的下标集合

export interface FieldSpec {
  key: string;
  control: FieldControl;
  required?: boolean;
  /** 布尔字段在服务端的默认值（cardtype 的 boolField 第三参）。缺省即 false。 */
  default?: boolean;
}

/** 通用可选字段，所有题型共用（cardtype.validateCommonOptional）。 */
const COMMON_OPTIONAL: FieldSpec[] = [
  { key: 'source_url', control: 'text' },
  { key: 'extra', control: 'textarea' },
];

export const CARD_KIND_FIELDS: Record<string, FieldSpec[]> = {
  basic: [
    { key: 'front', control: 'textarea', required: true },
    { key: 'back', control: 'textarea', required: true },
  ],
  basic_both: [
    { key: 'front', control: 'textarea', required: true },
    { key: 'back', control: 'textarea', required: true },
  ],
  cloze: [{ key: 'text', control: 'textarea', required: true }],
  list: [
    { key: 'prompt', control: 'textarea', required: true },
    { key: 'items', control: 'lines', required: true },
    { key: 'ordered', control: 'bool', default: false },
  ],
  typed: [
    { key: 'prompt', control: 'textarea', required: true },
    { key: 'answer', control: 'text', required: true },
    { key: 'accept', control: 'lines' },
    // 服务端默认忽略大小写与空白差异（cardtype.typedType 的 boolField 第三参是 true）：
    // 表单默认值必须与之相同，否则「没动过的开关保存一次」就把行为改成了 false。
    { key: 'ignore_case', control: 'bool', default: true },
    { key: 'ignore_whitespace', control: 'bool', default: true },
  ],
  numeric: [
    { key: 'prompt', control: 'textarea', required: true },
    { key: 'value', control: 'number', required: true },
    { key: 'unit', control: 'text' },
    { key: 'tolerance_absolute', control: 'number' },
    { key: 'tolerance_relative', control: 'number' },
  ],
  choice_single: [
    { key: 'question', control: 'textarea', required: true },
    { key: 'options', control: 'lines', required: true },
    { key: 'answer', control: 'index', required: true },
  ],
  choice_multi: [
    { key: 'question', control: 'textarea', required: true },
    { key: 'options', control: 'lines', required: true },
    { key: 'answers', control: 'indexes', required: true },
  ],
  true_false: [
    { key: 'statement', control: 'textarea', required: true },
    { key: 'answer', control: 'bool', required: true },
  ],
  short_answer: [
    { key: 'prompt', control: 'textarea', required: true },
    { key: 'reference', control: 'textarea' },
  ],
};

/** 题型的字段表（含通用可选字段）。未知题型返回空表，视图会退化为「无字段可编辑」。 */
export function fieldsForKind(kind: string): FieldSpec[] {
  const own = CARD_KIND_FIELDS[kind];
  return own ? [...own, ...COMMON_OPTIONAL] : [...COMMON_OPTIONAL];
}

/** 新建/换题型时的空白字段：布尔默认值按服务端语义给出（缺失即取默认）。 */
export function emptyFields(kind: string): Record<string, unknown> {
  const out: Record<string, unknown> = {};
  for (const spec of CARD_KIND_FIELDS[kind] ?? []) {
    if (spec.control === 'lines' || spec.control === 'indexes') out[spec.key] = [];
    else if (spec.control === 'bool') out[spec.key] = spec.default ?? false;
    else if (spec.control === 'number' || spec.control === 'index') out[spec.key] = '';
    else out[spec.key] = '';
  }
  return out;
}

/**
 * 把服务端字段收进表单模型：只保留该题型认识的键，并补上缺失的键。
 * 不认识的键（例如旧题型遗留）由调用方负责保留，避免静默丢数据。
 */
export function toFormFields(kind: string, raw: Record<string, unknown>): Record<string, unknown> {
  const out = emptyFields(kind);
  for (const key of Object.keys(out)) {
    const value = raw[key];
    if (value === undefined || value === null) continue;
    if (out[key] instanceof Array) out[key] = Array.isArray(value) ? [...value] : [];
    else if (typeof out[key] === 'boolean') out[key] = Boolean(value);
    else if (typeof out[key] === 'number') out[key] = typeof value === 'number' ? value : Number(value) || '';
    else out[key] = String(value);
  }
  return out;
}

/**
 * 表单模型 → 提交给服务端的字段。
 * 规则：空串与空数组一律省略（服务端的可选字段校验拒绝空串）；显式 false 必须保留
 * （AGENTS.md §2.3 第 9 条：带库默认值的布尔列不得被零值省略）。
 */
export function toPayloadFields(form: Record<string, unknown>): Record<string, unknown> {
  const out: Record<string, unknown> = {};
  for (const [key, value] of Object.entries(form)) {
    if (value === undefined || value === null) continue;
    if (typeof value === 'string') {
      const trimmed = value.trim();
      if (trimmed !== '') out[key] = trimmed;
      continue;
    }
    if (Array.isArray(value)) {
      const items = value.map((item) => String(item).trim()).filter((item) => item !== '');
      if (items.length > 0) out[key] = items;
      continue;
    }
    out[key] = value;
  }
  return out;
}

/** 数组字段 ↔ 多行文本（每行一项）。 */
export function linesToArray(text: string): string[] {
  return text.split('\n');
}

export function arrayToLines(value: unknown): string {
  return Array.isArray(value) ? value.map((item) => String(item)).join('\n') : '';
}
