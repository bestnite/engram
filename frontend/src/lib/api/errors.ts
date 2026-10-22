import { ApiClientError } from './types';

/**
 * Stable error code set for the registration/bootstrap endpoints (corresponds one-to-one with registrationErrorMessage in internal/web/auth_api.go on the Go side).
 * They all map to auth.error.<code> language pack keys, sharing the same copy as the SSR auth pages.
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
  // Stable codes for the login second step (TOTP) (Go side internal/api/errors.go):
  // invalid verification code, second-step credential missing or expired.
  'totp_invalid',
  'totp_challenge_expired',
]);

/**
 * Convert API errors into frontend language pack keys
 * Backend REST errors only return an English code and an English message; the frontend maps localized copy by stable code
 * and never exposes the backend's English message directly in the UI.
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
