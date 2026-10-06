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

    // 旧 SSR 编辑地址的别名：服务端 GET /decks/:id/notes/:nid 返回应用壳，客户端处理该精确 URL。
    const legacyEditMatch = matchRoute('/decks/42/notes/99', prodRoutes);
    expect(legacyEditMatch.route?.name).toBe('note-edit-nid');
    expect(legacyEditMatch.params).toEqual({ id: '42', noteId: '99' });

    const notesMatch = matchRoute('/decks/456/notes', prodRoutes);
    expect(notesMatch.route?.name).toBe('deck-notes');
    expect(notesMatch.params).toEqual({ id: '456' });
    const createMatch = matchRoute('/decks/456/notes/new', prodRoutes);
    expect(createMatch.route?.name).toBe('note-create');
    expect(createMatch.params).toEqual({ id: '456' });

    // 旧 SSR 新建地址的别名：服务端 GET /decks/:id/new-note 返回应用壳。
    const legacyCreateMatch = matchRoute('/decks/456/new-note', prodRoutes);
    expect(legacyCreateMatch.route?.name).toBe('note-create-legacy');
    expect(legacyCreateMatch.params).toEqual({ id: '456' });

    // 字面量 new 必须先于 :noteId 命中，不能被编辑别名吞掉。
    expect(matchRoute('/decks/456/notes/new', prodRoutes).route?.name).toBe('note-create');
    expect(matchRoute('/spa/review?deck=7&deck=9', prodRoutes).route?.name).toBe('review');
    expect(matchRoute('/import', prodRoutes).route?.name).toBe('import');
  });

  it('exposes the SPA login entry at /spa/login without removing SSR /login', async () => {
    const { routes: prodRoutes } = await import('../lib/router/routes');

    // SPA 迁移目标路径：服务端 GET /spa/login 返回应用壳并初始化双提交 cookie。
    const spaLogin = matchRoute('/spa/login', prodRoutes);
    expect(spaLogin.route?.name).toBe('spa-login');
    expect(spaLogin.params).toEqual({});

    // SSR /login 仍保留在同一前端路由表内（客户端导航用），不被遮蔽。
    const ssrLogin = matchRoute('/login', prodRoutes);
    expect(ssrLogin.route?.name).toBe('login');
  });
});
