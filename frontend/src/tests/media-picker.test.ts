import { describe, expect, it } from 'vitest';
import { en } from '../lib/i18n/locales/en';
import { zhCN } from '../lib/i18n/locales/zh-CN';

/** 媒体选择器与上传的中文/英文文案：键必须两套都在，上传失败的分支一个都不能少。 */
describe('SPA media picker catalog keys', () => {
  it('provides identical media.spa key sets in both catalogs', () => {
    const keys = Object.keys(en).filter((key) => key.startsWith('media.spa.')).sort();
    expect(keys.length).toBeGreaterThan(0);
    expect(Object.keys(zhCN).filter((key) => key.startsWith('media.spa.')).sort()).toEqual(keys);
  });

  it('provides identical localized upload keys in both catalogs', () => {
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
    expect(Object.keys(zhCN).filter((key) => key.startsWith('media.spa.upload.')).sort()).toEqual(keys);
  });
});
