import { ApiClientError } from './api';

/**
 * Localized message key for a failed media upload.
 *
 * The server answers a rejected upload with a stable error code rather than a sentence, and every
 * upload entry in the editor (the image dialog on a field, the audio action in the shortcode
 * composer) posts to the same endpoint. One map keeps the wording of a given failure identical in
 * both places, and an unknown code still produces a sentence instead of a bare code.
 */
export function uploadErrorKeyFor(err: unknown): string {
  const code = err instanceof ApiClientError ? err.code : '';
  const keys: Record<string, string> = {
    media_too_large: 'media.upload.too_large',
    media_mime_not_allowed: 'media.upload.mime_not_allowed',
    media_magic_mismatch: 'media.upload.magic_mismatch',
    media_quota_exceeded: 'media.upload.quota_exceeded',
    media_missing_file: 'media.upload.file_required',
  };
  return keys[code] || 'media.upload.failed';
}
