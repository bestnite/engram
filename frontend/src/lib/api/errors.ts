import { ApiClientError } from './types';

/**
 * 注册/引导接口的稳定错误 code 集合（与 Go 侧 internal/web/spa_auth.go 的 registrationErrorMessage 一一对应）。
 * 它们统一映射到 auth.error.<code> 语言包键，与 SSR 认证页共用同一批文案。
 */
const authErrorCodes = new Set([
  'username_required',
  'email_required',
  'email_invalid',
  'password_required',
  'password_too_short',
  'password_too_long',
  'password_too_common',
  'email_domain_not_allowed',
  'invite_required',
  'invite_invalid',
  'registration_closed',
  'create_failed',
  // 登录第二步（TOTP）的稳定 code（Go 侧 internal/api/errors.go）：
  // 验证码无效、第二步凭据缺失或过期。
  'totp_invalid',
  'totp_challenge_expired',
]);

/**
 * 将 API 错误转换为前端语言包 key
 * 后端 REST 错误只返回英文 code 与英文 message，前端根据稳定 code 映射本地化文案，
 * 绝不直接把后端的英文 message 暴露在界面上。
 */
export function getApiErrorMessageKey(error: unknown): string {
  if (error instanceof ApiClientError) {
    if (error.code === 'csrf_failed' || error.code === 'csrf_no_session') {
      return 'error.csrf_failed';
    }
    if (error.isCsrfError) {
      return 'error.csrf_failed';
    }
    if (error.isInvalidCredentials) {
      return 'error.invalid_credentials';
    }
    if (error.isUserDisabled) {
      return 'error.user_disabled';
    }
    if (error.isTotpRequired) {
      return 'error.totp_required';
    }
    if (authErrorCodes.has(error.code)) {
      return `auth.error.${error.code}`;
    }
    if (error.isUnauthorized) {
      return 'error.unauthorized';
    }
    if (error.isForbidden) {
      return 'error.forbidden';
    }
    if (error.isConflict) {
      return 'error.conflict';
    }
    if (error.isRateLimited) {
      return 'error.rate_limited';
    }
    if (error.isNotFound) {
      return 'error.not_found';
    }
    if (error.isNetworkError) {
      return 'error.network';
    }
    if (error.code === 'invalid_response') {
      return 'error.invalid_response';
    }
  }
  return 'error.unknown';
}
