import type { AdminMailTemplateDefault, AdminMailTemplateRow } from './api';

/**
 * Initial value of the mail template edit box.
 *
 * Use the custom version when there is one; otherwise **pre-fill the built-in default body**. The latter is no small convenience: leaving it blank would mean
 * the admin has to rewrite a whole message in an empty box, while in the vast majority of cases they are just editing those default sentences,
 * so pre-filling makes "edit" the only action.
 *
 * It is a pure function because `$effect` does not run under server rendering - if the edit box's initial value could only be set by a side effect, it could only
 * be verified in a browser; here the input and output are unit-testable.
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
