import { describe, it, expect, beforeEach } from 'vitest';
import { render } from 'svelte/server';
import DecksView from '../lib/views/DecksView.svelte';
import { setLocale } from '../lib/i18n';

/**
 * Entry-point assertions for the deck list page.
 *
 * The deck package import page, route and API have always existed (/import → ImportView,
 * POST /api/v1/decks/import); the only thing ever missing was the entry link: in SSR days
 * it hung off the deck list header and was not carried over in the SPA migration, leaving
 * the page reachable only by typing the URL. This pins that the list header always
 * renders that link.
 */
describe('DecksView import entry', () => {
  beforeEach(() => {
    setLocale('zh-CN');
  });

  it('links the deck-list header to the import page', () => {
    const { html } = render(DecksView);
    expect(html).toContain('data-testid="decks-import-open"');
    expect(html).toContain('href="/import"');
    expect(html).toContain('导入卡组包');
  });
});
