import { describe, it, expect } from 'vitest';
import { matchRoute, parseQuery } from '../lib/router';
import type { RouteDefinition } from '../lib/router/types';

describe('SPA router matching and query parsing', () => {
  it('parses query strings correctly', () => {
    expect(parseQuery('')).toEqual({});
    expect(parseQuery('?lang=en&deck=42')).toEqual({ lang: 'en', deck: '42' });
    expect(parseQuery('lang=zh-CN')).toEqual({ lang: 'zh-CN' });
  });

  it('matches exact routes and returns params and query', () => {
    const dummyComponent = {} as unknown as RouteDefinition['component'];
    const testRoutes: RouteDefinition[] = [
      { path: '/', name: 'home', component: dummyComponent },
      { path: '/decks', name: 'decks', component: dummyComponent },
      { path: '/decks/:id', name: 'deck-detail', component: dummyComponent },
      { path: '/decks/:id/notes/:nid', name: 'note-edit', component: dummyComponent },
    ];

    // 静态路径匹配
    const homeMatch = matchRoute('/', testRoutes);
    expect(homeMatch.route?.name).toBe('home');
    expect(homeMatch.params).toEqual({});

    // 带参数的路由匹配
    const deckMatch = matchRoute('/decks/42?lang=en', testRoutes);
    expect(deckMatch.route?.name).toBe('deck-detail');
    expect(deckMatch.params).toEqual({ id: '42' });
    expect(deckMatch.query).toEqual({ lang: 'en' });

    // 多段动态参数匹配
    const noteMatch = matchRoute('/decks/42/notes/99', testRoutes);
    expect(noteMatch.route?.name).toBe('note-edit');
    expect(noteMatch.params).toEqual({ id: '42', nid: '99' });

    // 未知路由（404）
    const notFoundMatch = matchRoute('/unknown/route', testRoutes);
    expect(notFoundMatch.route).toBeNull();
    expect(notFoundMatch.path).toBe('/unknown/route');
  });

  it('matches actual production deck routes correctly', async () => {
    const { routes: prodRoutes } = await import('../lib/router/routes');

    const detailMatch = matchRoute('/decks/123', prodRoutes);
    expect(detailMatch.route?.name).toBe('deck-detail');
    expect(detailMatch.params).toEqual({ id: '123' });

    const editMatch = matchRoute('/decks/42/notes/99/edit', prodRoutes);
    expect(editMatch.route?.name).toBe('note-edit');
    expect(editMatch.params).toEqual({ id: '42', noteId: '99' });

    const notesMatch = matchRoute('/decks/456/notes', prodRoutes);
    expect(notesMatch.route?.name).toBe('deck-notes');
    expect(notesMatch.params).toEqual({ id: '456' });
    const createMatch = matchRoute('/decks/456/notes/new', prodRoutes);
    expect(createMatch.route?.name).toBe('note-create');
    expect(createMatch.params).toEqual({ id: '456' });
    expect(matchRoute('/spa/review?deck=7&deck=9', prodRoutes).route?.name).toBe('review');
  });
});
