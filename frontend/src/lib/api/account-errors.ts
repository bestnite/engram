import { ApiClientError } from './types';

/**
 * Stable error code set for account security and mail flows.
 * Corresponds one-to-one with the codes returned by internal/web/account.go on the Go side; all mapped to account.error.<code>
 * language pack keys, never showing the backend's English message directly in the UI.
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
 * Convert account security/mail endpoint errors into frontend language pack keys.
 * Stable code first; an unknown code falls back to a generic key derived from the status code, and the backend copy is never echoed.
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
