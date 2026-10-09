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
