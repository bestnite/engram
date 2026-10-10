/**
 * 字段规格与表单模型转换。
 *
 * 字段表（顺序、控件形态、必填性、默认值）由服务端的题型自描述给出（GET /api/v1/card-types，
 * 契约在 internal/cardtype/describe.go）。本文件只保留与传输无关的纯函数：控件词汇表、
 * 空表单初始化、表单模型与提交负载的相互转换。视图不再按题型写十套表单。
 */
import type { CardTypeField, CardTypeFieldControl } from './api/types';

/** 控件词汇表：与服务端 FieldSpec.control 的取值集合逐项一致。 */
export type FieldControl = CardTypeFieldControl;

/** 字段规格即服务端下发的形态（键、控件、必填、默认值）。 */
export type FieldSpec = CardTypeField;

/** 新建/换题型时的空白字段：布尔默认值按服务端语义给出（缺失即取默认 false）。 */
export function emptyFields(specs: readonly FieldSpec[]): Record<string, unknown> {
  const out: Record<string, unknown> = {};
  for (const spec of specs) {
    if (spec.control === 'lines' || spec.control === 'indexes') out[spec.key] = [];
    else if (spec.control === 'bool') out[spec.key] = spec.default === true;
    else if (spec.control === 'number' || spec.control === 'index') out[spec.key] = '';
    else out[spec.key] = '';
  }
  return out;
}

/**
 * 把服务端字段收进表单模型：只保留该题型认识的键，并补上缺失的键。
 * 不认识的键（例如旧题型遗留）由调用方负责保留，避免静默丢数据。
 */
export function toFormFields(
  specs: readonly FieldSpec[],
  raw: Record<string, unknown>
): Record<string, unknown> {
  const out = emptyFields(specs);
  for (const spec of specs) {
    const key = spec.key;
    const value = raw[key];
    if (value === undefined || value === null) continue;
    if (spec.control === 'lines' || spec.control === 'indexes') out[key] = Array.isArray(value) ? [...value] : [];
    else if (spec.control === 'bool') out[key] = Boolean(value);
    // 数值与下标按控件判定，不能看空表单里的初值：空表单把它们初始化成 ''（「未填」），
    // 曾经按初值类型分支，导致服务端的 42 被转成字符串 "42"——输入框显示为空，
    // 预览请求也因类型不符被服务端以 400 拒绝（界面上表现为「补全必填字段后即可预览」）。
    else if (spec.control === 'number' || spec.control === 'index') out[key] = toNumberOrEmpty(value);
    else out[key] = String(value);
  }
  return out;
}

/** 数字原样保留；能完整解析成有限数的字符串转成数字；其余视为未填。 */
function toNumberOrEmpty(value: unknown): number | '' {
  if (typeof value === 'number') return Number.isFinite(value) ? value : '';
  if (typeof value === 'string' && value.trim() !== '') {
    const parsed = Number(value);
    return Number.isFinite(parsed) ? parsed : '';
  }
  return '';
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
      // 只修剪字符串项；数字项（多选题的答案下标）原样保留。服务端按整数解析下标，
      // 统一转成字符串会让 ["0","2"] 被以 400 拒绝，预览与保存都因此失败。
      const items = value.flatMap((item) => {
        if (item === undefined || item === null) return [];
        if (typeof item === 'string') {
          const trimmed = item.trim();
          return trimmed === '' ? [] : [trimmed];
        }
        return [item];
      });
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
