import { COMMON_TIMEZONES } from './api/validation';
import type { SelectOption } from './components/ui/options';

/**
 * 浏览器支持的 IANA 时区全量。
 *
 * 用 `Intl.supportedValuesOf('timeZone')` 而不是在仓库里再抄一份清单：它返回的正是当前
 * 浏览器能解析的名字，与前端校验（validateTimezone 用 Intl.DateTimeFormat 试解析）同源，
 * 手抄一份必然漂移。旧浏览器没有这个 API，返回空数组由常用列表兜底。
 */
function browserTimeZones(): string[] {
  try {
    const supported = (Intl as { supportedValuesOf?: (key: string) => string[] }).supportedValuesOf;
    if (typeof supported !== 'function') return [];
    return supported.call(Intl, 'timeZone');
  } catch {
    return [];
  }
}

/**
 * 构造时区候选：UTC 与常用时区置顶（按常用顺序），其余按字母序在后。
 *
 * 三条不可省：① `Intl.supportedValuesOf` 不含 UTC，必须显式补上；② 去重，常用列表
 * 本来就是全量列表的子集；③ **一定并入 currentValue** —— 库里可能存着本浏览器不认识
 * 的历史名字，不并进来这个字段就会显示成空，用户一保存就被改写。
 */
export function timezoneOptions(currentValue?: string): SelectOption[] {
  const pinned = ['UTC', ...COMMON_TIMEZONES.filter((tz) => tz !== 'UTC')];
  const seen = new Set<string>();
  const out: SelectOption[] = [];

  const push = (tz: string): void => {
    const name = tz.trim();
    if (name === '' || seen.has(name)) return;
    seen.add(name);
    out.push({ value: name, label: name });
  };

  pinned.forEach(push);
  if (currentValue) push(currentValue);
  browserTimeZones()
    .slice()
    .sort((a, b) => a.localeCompare(b))
    .forEach(push);
  return out;
}
