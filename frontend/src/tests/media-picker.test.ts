import { describe, expect, it } from 'vitest';
import { en } from '../lib/i18n/locales/en';

/**
 * 上传链路的文案键：每一条失败分支都要有对应键，一个都不能少。
 *
 * 下面钉的是完整清单——增删键都要在这里显式改一次。两套语言包的键集合一致性由
 * parity.test.ts 全局保证，这里不再逐块重复断言同一件事。
 */
describe('SPA media picker catalog keys', () => {
  it('pins the complete set of media upload keys', () => {
    const keys = Object.keys(en).filter((key) => key.startsWith('media.spa.upload.')).sort();
    expect(keys).toEqual([
      'media.spa.upload.button',
      'media.spa.upload.failed',
      'media.spa.upload.file_required',
      'media.spa.upload.inserted',
      'media.spa.upload.label',
      'media.spa.upload.magic_mismatch',
      'media.spa.upload.mime_not_allowed',
      'media.spa.upload.quota_exceeded',
      'media.spa.upload.too_large',
      'media.spa.upload.uploading',
    ]);
  });
});
