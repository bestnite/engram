import { describe, expect, it } from 'vitest';
import { readFileSync } from 'node:fs';
import { fileURLToPath } from 'node:url';
import { en } from '../lib/i18n/locales/en';
import { zhCN } from '../lib/i18n/locales/zh-CN';

function read(relative: string): string {
  return readFileSync(fileURLToPath(new URL(relative, import.meta.url)), 'utf8');
}

describe('SPA media picker editor integration', () => {
  it('drives the picker from the JSON media library with pagination and lazy original images', () => {
    // 选择器不再拉取 SSR HTML 片段，而是直接消费 GET /api/v1/media 的 JSON。
    const picker = read('../lib/components/MediaPicker.svelte');
    expect(picker).toContain('apiClient.listMedia(');
    expect(picker).not.toContain('getMediaPickerPage');
    expect(picker).not.toContain('DOMParser');
    expect(picker).toContain('data-testid="spa-media-picker-toggle"');
    expect(picker).toContain('data-testid="spa-media-item-{item.sha256}"');
    expect(picker).toContain('data-testid="spa-media-next"');
    expect(picker).toContain('loading="lazy"');
    // 缩略预览与插入都用服务端给出的 url（/media/<sha256>），前端不自己拼路径。
    expect(picker).toContain('onselect(item.url)');
    expect(picker).toContain('src={item.url}');
    expect(picker).toContain("{$t('media.spa.heading')}");
    expect(picker).toContain("{$t('media.spa.next')}");
    expect(picker).not.toContain('{@html');
  });

  it('mounts the picker from the note editor and keeps the shared insert path', () => {
    const view = read('../lib/views/NoteEditView.svelte');
    expect(view).toContain('<MediaPicker onselect={insertMedia} />');
    expect(view).toContain('![](${url})');
    expect(view).toContain("{$t('media.spa.field')}");
    expect(view).not.toContain('apiClient.listMedia');
    expect(view).not.toContain('{@html fields');
  });

  it('wires an upload control to the deck media endpoint and reuses the safe insert path', () => {
    const view = read('../lib/views/NoteEditView.svelte');
    // 上传成功后追加返回的 url，且与选择器共用同一段插入逻辑（只拼 ![](<url>)）。
    expect(view).toContain('apiClient.uploadDeckMedia(deckId, file)');
    expect(view).toContain('insertMedia(result.url)');
    expect(view).toContain('data-testid="spa-media-upload-input"');
    expect(view).toContain('testId="spa-media-upload-submit"');
    expect(view).toContain('data-testid="spa-media-upload-status"');
    expect(view).toContain('data-testid="spa-media-upload-error"');
    expect(view).toContain("{$t('media.spa.upload.label')}");
    // 失败按稳定 code 映射本地化 key，而不是把后端 message 或返回数据渲染成 HTML。
    expect(view).toContain('uploadErrorKeyFor');
    expect(view).toContain("media_quota_exceeded: 'media.spa.upload.quota_exceeded'");
    expect(view).not.toContain('{@html result');
    expect(view).not.toContain('{@html upload');
  });

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
