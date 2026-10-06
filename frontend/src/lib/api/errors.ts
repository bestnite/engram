import { ApiClientError } from './types';

/**
 * 将 API 错误转换为前端语言包 key（DESIGN.md §8.3）
 * 后端 REST 错误只返回英文 code 与英文 message，前端根据稳定 code 映射本地化文案，
 * 绝不直接把后端的英文 message 暴露在界面上。
 */
export function getApiErrorMessageKey(error: unknown): string {
  if (error instanceof ApiClientError) {
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
