import { describe, it, expect, beforeEach, afterEach } from 'vitest';
import { render } from 'svelte/server';
import AppSidebar from '../lib/components/shell/AppSidebar.svelte';
import { authStore } from '../lib/auth';
import { routeStore, matchRoute } from '../lib/router';
import { setLocale } from '../lib/i18n';
import { buildSidebarDecks, sidebarDecks, SIDEBAR_DECK_LIMIT } from '../lib/components/shell/sidebar';
import type { Deck, User } from '../lib/api';

function deck(id: string, name: string, extra: Partial<Deck> = {}): Deck {
  return {
    id,
    name,
    description: '',
    new_per_day: 20,
    reviews_per_day: 200,
    preset_id: '1',
    created_at: '2026-01-01T00:00:00Z',
    role: 'owner',
    ...extra,
  };
}

function signIn(role: string): void {
  const user: User = { id: '1', username: 'demo', email: 'demo@example.com', role, locale: 'zh-CN' };
  authStore.set({ initialized: true, loading: false, authenticated: true, user, error: null });
}

describe('buildSidebarDecks', () => {
  it('orders decks by due count, then by name, and sums the total', () => {
    const state = buildSidebarDecks(
      [deck('a', 'Beta'), deck('b', 'Alpha'), deck('c', 'Gamma')],
      {
        decks: [
          { deck_id: 'a', new_count: 1, review_count: 2 },
          { deck_id: 'c', new_count: 10, review_count: 0 },
        ],
      }
    );
    expect(state.decks.map((d) => d.name)).toEqual(['Gamma', 'Beta', 'Alpha']);
    expect(state.decks.map((d) => d.due)).toEqual([10, 3, 0]);
    expect(state.totalDue).toBe(13);
  });

  it('drops archived decks and caps the list', () => {
    const many = Array.from({ length: SIDEBAR_DECK_LIMIT + 3 }, (_, i) => deck(String(i), `Deck ${i}`));
    many.push(deck('archived', 'Archived', { archived_at: '2026-02-01T00:00:00Z' }));
    const state = buildSidebarDecks(many, { decks: [] });
    expect(state.decks).toHaveLength(SIDEBAR_DECK_LIMIT);
    expect(state.decks.some((d) => d.id === 'archived')).toBe(false);
  });

  it('shows names without numbers when queue counts are unavailable', () => {
    const state = buildSidebarDecks([deck('a', 'Alpha')], null);
    expect(state.decks[0]?.due).toBeNull();
    expect(state.totalDue).toBeNull();
  });
});

describe('AppSidebar', () => {
  beforeEach(() => {
    setLocale('zh-CN');
    sidebarDecks.set({ decks: [], totalDue: null });
    routeStore.set(matchRoute('/'));
  });

  afterEach(() => {
    authStore.set({ initialized: false, loading: false, authenticated: false, user: null, error: null });
  });

  it('hides the admin entry from a non-admin user', () => {
    signIn('user');
    const { html } = render(AppSidebar);
    expect(html).not.toContain('href="/admin/users"');
    expect(html).toContain('href="/settings"');
  });

  it('shows the admin entry to an admin, pointing at the first admin page', () => {
    signIn('admin');
    const { html } = render(AppSidebar);
    expect(html).toContain('href="/admin/users"');
  });

  it('lists deck shortcuts and marks the current deck as the active page', () => {
    signIn('user');
    sidebarDecks.set({ decks: [{ id: 'd1', name: '日语 N2 词汇', due: 36 }], totalDue: 36 });
    routeStore.set(matchRoute('/decks/d1'));
    const { html } = render(AppSidebar);
    expect(html).toContain('data-testid="sidebar-decks"');
    expect(html).toMatch(/href="\/decks\/d1"[^>]*aria-current="page"/);
    // 当前卡组已列在下面时，父项「卡组」不再同时高亮。
    expect(html).not.toMatch(/href="\/decks"[^>]*aria-current="page"/);
    expect(html).toContain('36 张待复习');
  });

  it('hides labels and deck shortcuts when collapsed, keeping accessible names', () => {
    signIn('user');
    sidebarDecks.set({ decks: [{ id: 'd1', name: 'Alpha', due: 2 }], totalDue: 2 });
    const { html } = render(AppSidebar, { props: { collapsed: true, onToggleCollapse: () => {} } });
    expect(html).not.toContain('data-testid="sidebar-decks"');
    expect(html).toContain('title="统计"');
    expect(html).toContain('展开侧边栏');
  });
});
