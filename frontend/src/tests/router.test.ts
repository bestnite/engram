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
    // 旧 SPA 复习地址仍在路由表内（兼容既有深链），但规范入口是 /review。
    expect(matchRoute('/spa/review?deck=7&deck=9', prodRoutes).route?.name).toBe('review-spa');
    expect(matchRoute('/import', prodRoutes).route?.name).toBe('import');
  });

  // 回归：服务端 GET /review 已返回应用壳（5a3b489 切流），客户端路由必须能处理这个精确
  // URL，否则首页「开始复习」链接与直接访问/刷新都会落到 NotFoundView（实测 404）。
  it('serves the canonical review route at /review and keeps the /spa alias', async () => {
    const { routes: prodRoutes } = await import('../lib/router/routes');

    const canonical = matchRoute('/review', prodRoutes);
    expect(canonical.route?.name).toBe('review');
    expect(canonical.params).toEqual({});
    // 卡组范围参数不影响匹配，只进 query。
    const scoped = matchRoute('/review?deck=7&deck=9', prodRoutes);
    expect(scoped.route?.name).toBe('review');
    expect(scoped.query).toEqual({ deck: '9' });

    // 迁移期旧地址仍可用，但不得遮蔽规范路由。
    const alias = matchRoute('/spa/review', prodRoutes);
    expect(alias.route?.name).toBe('review-spa');

    // 未匹配路径仍是 404 视图。
    expect(matchRoute('/review/unknown', prodRoutes).route).toBeNull();
  });

  it('exposes the SPA login entry at /login and keeps the /spa/login alias', async () => {
    const { routes: prodRoutes } = await import('../lib/router/routes');

    // 规范路径 /login 由服务端 GET 返回应用壳并初始化双提交 cookie；/spa/login 是迁移期别名。
    const spaLogin = matchRoute('/spa/login', prodRoutes);
    expect(spaLogin.route?.name).toBe('spa-login');
    expect(spaLogin.params).toEqual({});

    // SSR /login 仍保留在同一前端路由表内（客户端导航用），不被遮蔽。
    const ssrLogin = matchRoute('/login', prodRoutes);
    expect(ssrLogin.route?.name).toBe('login');
  });
});

describe('auth entry routes after the SPA cutover', () => {
  it('serves /login, /register and /setup canonically and keeps the /spa aliases', async () => {
    const { routes: prodRoutes } = await import('../lib/router/routes');
    expect(matchRoute('/login', prodRoutes).route?.name).toBe('login');
    expect(matchRoute('/register', prodRoutes).route?.name).toBe('register');
    expect(matchRoute('/setup', prodRoutes).route?.name).toBe('setup');
    // 迁移期别名保留，既有深链不失效。
    expect(matchRoute('/spa/login', prodRoutes).route?.name).toBe('spa-login');
    expect(matchRoute('/spa/register', prodRoutes).route?.name).toBe('spa-register');
    expect(matchRoute('/spa/setup', prodRoutes).route?.name).toBe('spa-setup');
  });
});
