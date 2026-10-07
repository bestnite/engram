import { ApiClientError } from './types';

/**
 * 账号安全与邮件流程的稳定错误 code 集合。
 * 与 Go 侧 internal/web/spa_account.go 返回的 code 一一对应；统一映射到 account.error.<code>
 * 语言包键，绝不把后端的英文 message 直接展示在界面上。
 */
const accountErrorCodes = new Set([
  'token_expired',
  'token_used',
  'token_invalid',
  'mail_not_configured',
  'email_invalid',
  'email_same',
  'email_taken',
  'email_conflict',
  'password_required',
  'password_too_short',
  'password_too_long',
  'password_too_common',
  'rate_limited',
]);

/**
 * 把账号安全/邮件接口错误转换为前端语言包 key。
 * 稳定 code 优先；未知 code 回落到按状态码推导的通用键，绝不回显后端文案。
 */
export function getAccountErrorMessageKey(error: unknown): string {
  if (error instanceof ApiClientError) {
    if (accountErrorCodes.has(error.code)) {
      return `account.error.${error.code}`;
    }
    if (error.isRateLimited) {
      return 'error.rate_limited';
    }
    if (error.isUnauthorized) {
      return 'error.unauthorized';
    }
    if (error.isForbidden) {
      return 'error.csrf_failed';
    }
    if (error.isNotFound) {
      return 'account.error.token_invalid';
    }
    if (error.isNetworkError) {
      return 'error.network';
    }
  }
  return 'error.unknown';
}
