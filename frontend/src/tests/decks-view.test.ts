import { describe, it, expect, beforeEach } from 'vitest';
import { render } from 'svelte/server';
import DecksView from '../lib/views/DecksView.svelte';
import { setLocale } from '../lib/i18n';

/**
 * 卡组列表页的入口断言。
 *
 * 卡组包导入的页面、路由与接口一直都在（/import → ImportView，POST /api/v1/decks/import），
 * 唯一缺过的是入口链接：SSR 时期它挂在卡组列表头部，SPA 迁移时没有被带过来，页面于是只能
 * 靠手输地址访问。这里钉住列表头部始终渲染这个链接。
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
