import { describe, expect, it } from 'vitest';
import { en } from '../lib/i18n/locales/en';

/**
 * Message keys for the upload path: every failure branch needs its own key, without
 * exception.
 *
 * What follows pins the complete list — adding or removing a key means editing it here
 * explicitly. Consistency of the key set across both language packs is guaranteed globally
 * by parity.test.ts, so that is not re-asserted block by block here.
 */
describe('SPA media picker catalog keys', () => {
  it('pins the complete set of media upload keys', () => {
    const keys = Object.keys(en).filter((key) => key.startsWith('media.upload.')).sort();
    expect(keys).toEqual([
      'media.upload.button',
      'media.upload.failed',
      'media.upload.file_required',
      'media.upload.inserted',
      'media.upload.label',
      'media.upload.magic_mismatch',
      'media.upload.mime_not_allowed',
      'media.upload.quota_exceeded',
      'media.upload.too_large',
      'media.upload.uploading',
    ]);
  });
});
