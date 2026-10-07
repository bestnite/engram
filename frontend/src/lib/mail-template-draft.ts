import type { AdminMailTemplateDefault, AdminMailTemplateRow } from './api';

/**
 * 邮件模板编辑框的初值（DESIGN.md §4.7）。
 *
 * 有自定义版本就用它；没有就**预填内置默认正文**。后者不是「顺手加的小便利」：留空意味着
 * 管理员要对着一个空框重写一封完整的信，而绝大多数情况下他本来就是在默认的那几句话上改，
 * 预填把「改」变成了唯一动作。
 *
 * 抽成纯函数是因为服务端渲染下 `$effect` 不跑——编辑框的初值如果只能靠副作用设置，就只
 * 能在浏览器里验证；这里的输入输出是可以单测的。
 */
export interface MailTemplateDraft {
  subject: string;
  body: string;
}

export function draftFor(
  row: AdminMailTemplateRow | null | undefined,
  builtin: AdminMailTemplateDefault | null | undefined
): MailTemplateDraft {
  return {
    subject: row?.subject ?? builtin?.subject ?? '',
    body: row?.body_md ?? builtin?.body_md ?? '',
  };
}
