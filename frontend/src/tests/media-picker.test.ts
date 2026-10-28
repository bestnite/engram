import { describe, expect, it, beforeEach } from 'vitest';
import { render } from 'svelte/server';
import MediaPicker from '../lib/components/MediaPicker.svelte';
import { setLocale } from '../lib/i18n';
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
      'media.upload.audio_only',
      'media.upload.failed',
      'media.upload.file_required',
      'media.upload.image_only',
      'media.upload.inserted',
      'media.upload.magic_mismatch',
      'media.upload.mime_not_allowed',
      'media.upload.quota_exceeded',
      'media.upload.too_large',
      'media.upload.uploading',
    ]);
  });
});

/**
 * Two shapes of the same picker: a dialog shows the library as its own content, while a panel
 * nested in another surface keeps the toggle that opens it. The shape is fixed by the host, so it
 * is visible in the first render alone.
 */
describe('MediaPicker presence', () => {
  beforeEach(() => {
    setLocale('zh-CN');
  });

  it('renders the library at once, with no toggle, when its host is the dialog', () => {
    const { html } = render(MediaPicker, {
      props: { onselect: () => {}, embedded: true, idPrefix: 'note-insert-image' },
    });

    expect(html).toContain('data-testid="note-insert-image-picker"');
    expect(html).not.toContain('data-testid="note-insert-image-picker-toggle"');
    // The first paint is the loading state: before the page has been read, an "empty library" sentence
    // would be a claim about a page nobody looked at.
    expect(html).toContain('data-testid="note-insert-image-loading"');
    expect(html).not.toContain('媒体库为空');
  });

  it('keeps the toggle for a picker that is opened on demand', () => {
    const { html } = render(MediaPicker, { props: { onselect: () => {} } });

    expect(html).toContain('data-testid="media-picker-toggle"');
    expect(html).not.toContain('data-testid="media-picker"');
    expect(html).toContain('从媒体库选择');
  });
});
