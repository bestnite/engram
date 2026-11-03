import { ApiClientError } from './api';

/**
 * Localized message key for a failed media upload.
 *
 * The server answers a rejected upload with a stable error code rather than a sentence, and every
 * upload entry in the editor (the image dialog on a field) posts to the same endpoint. One map keeps the wording of a
 * given failure identical everywhere, and an unknown code still produces a sentence instead of a bare code.
 */
export function uploadErrorKeyFor(err: unknown): string {
  const code = err instanceof ApiClientError ? err.code : '';
  const keys: Record<string, string> = {
    media_too_large: 'media.upload.too_large',
    media_magic_mismatch: 'media.upload.magic_mismatch',
    media_quota_exceeded: 'media.upload.quota_exceeded',
    media_missing_file: 'media.upload.file_required',
  };
  return keys[code] || 'media.upload.failed';
}

/**
 * Localized message key for a failed media-picker selection.
 *
 * Choosing a library item registers its deck-scoped name before anything is inserted. The server answers a refusal with
 * the same stable media codes it uses for uploads, and the wording rule is the same: a known code gets specific copy and
 * an unknown one still produces a sentence instead of a bare code.
 */
export function pickerErrorKeyFor(err: unknown): string {
  const code = err instanceof ApiClientError ? err.code : '';
  const keys: Record<string, string> = {
    media_not_found: 'media.picker.error_not_found',
    media_not_readable: 'media.picker.error_not_readable',
    invalid_response: 'media.picker.error_invalid_response',
  };
  return keys[code] || 'media.picker.error_failed';
}
