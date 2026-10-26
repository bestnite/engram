import { describe, it, expect } from 'vitest';
import { matchRoute, parseQuery, viewKey } from '../lib/router';
import type { RouteDefinition } from '../lib/router/types';
import ShortcodesHelpView from '../lib/views/ShortcodesHelpView.svelte';

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

    // Static path matching
    const homeMatch = matchRoute('/', testRoutes);
    expect(homeMatch.route?.name).toBe('home');
    expect(homeMatch.params).toEqual({});

    // Route matching with params
    const deckMatch = matchRoute('/decks/42?lang=en', testRoutes);
    expect(deckMatch.route?.name).toBe('deck-detail');
    expect(deckMatch.params).toEqual({ id: '42' });
    expect(deckMatch.query).toEqual({ lang: 'en' });

    // Multi-segment dynamic param matching
    const noteMatch = matchRoute('/decks/42/notes/99', testRoutes);
    expect(noteMatch.route?.name).toBe('note-edit');
    expect(noteMatch.params).toEqual({ id: '42', nid: '99' });

    // Unknown route (404)
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

    // Alias for the old SSR edit URL: the server returns the app shell for GET /decks/:id/notes/:nid, and the client handles that exact URL.
    const legacyEditMatch = matchRoute('/decks/42/notes/99', prodRoutes);
    expect(legacyEditMatch.route?.name).toBe('note-edit-nid');
    expect(legacyEditMatch.params).toEqual({ id: '42', noteId: '99' });

    const notesMatch = matchRoute('/decks/456/notes', prodRoutes);
    expect(notesMatch.route?.name).toBe('deck-notes');
    expect(notesMatch.params).toEqual({ id: '456' });
    const createMatch = matchRoute('/decks/456/notes/new', prodRoutes);
    expect(createMatch.route?.name).toBe('note-create');
    expect(createMatch.params).toEqual({ id: '456' });

    // Alias for the old SSR create URL: the server returns the app shell for GET /decks/:id/new-note.
    const legacyCreateMatch = matchRoute('/decks/456/new-note', prodRoutes);
    expect(legacyCreateMatch.route?.name).toBe('note-create-legacy');
    expect(legacyCreateMatch.params).toEqual({ id: '456' });

    // The literal new must match before :noteId and must not be swallowed by the edit alias.
    expect(matchRoute('/decks/456/notes/new', prodRoutes).route?.name).toBe('note-create');
    expect(matchRoute('/import', prodRoutes).route?.name).toBe('import');
  });

  // Regression: the server already returns the app shell for GET /review (switched over in 5a3b489), so the client router must handle that exact
  // URL, otherwise the home page's "start review" link and direct visits/reloads land on NotFoundView (observed as a 404).
  it('serves the canonical review route at /review', async () => {
    const { routes: prodRoutes } = await import('../lib/router/routes');

    const canonical = matchRoute('/review', prodRoutes);
    expect(canonical.route?.name).toBe('review');
    expect(canonical.params).toEqual({});
    // The deck scope param does not affect matching; it only goes into the query.
    const scoped = matchRoute('/review?deck=7&deck=9', prodRoutes);
    expect(scoped.route?.name).toBe('review');
    expect(scoped.query).toEqual({ deck: '9' });

    // An unmatched path is still the 404 view.
    expect(matchRoute('/review/unknown', prodRoutes).route).toBeNull();
  });

  it('exposes the login entry at /login', async () => {
    const { routes: prodRoutes } = await import('../lib/router/routes');

    // The server returns the app shell for GET /login and initialises the double-submit cookie.
    const login = matchRoute('/login', prodRoutes);
    expect(login.route?.name).toBe('login');
    expect(login.params).toEqual({});
  });
});

describe('auth entry routes after the SPA cutover', () => {
  it('serves /login, /register and /setup canonically', async () => {
    const { routes: prodRoutes } = await import('../lib/router/routes');
    expect(matchRoute('/login', prodRoutes).route?.name).toBe('login');
    expect(matchRoute('/register', prodRoutes).route?.name).toBe('register');
    expect(matchRoute('/setup', prodRoutes).route?.name).toBe('setup');
  });
});

describe('shortcode reference route', () => {
  it('serves /help/shortcodes from the help view', async () => {
    const { routes: prodRoutes } = await import('../lib/router/routes');
    const match = matchRoute('/help/shortcodes', prodRoutes);

    expect(match.route?.name).toBe('help-shortcodes');
    expect(match.route?.component).toBe(ShortcodesHelpView);
    expect(match.params).toEqual({});
    // A neighbouring help path is not swallowed by the new route.
    expect(matchRoute('/help', prodRoutes).route).toBeNull();
  });
});

describe('view rebuild key', () => {
  const dummyComponent = {} as unknown as RouteDefinition['component'];
  const testRoutes: RouteDefinition[] = [
    { path: '/decks/:id', name: 'deck-detail', component: dummyComponent },
    { path: '/decks/:id/notes/:nid', name: 'note-edit', component: dummyComponent },
    { path: '/review', name: 'review', component: dummyComponent },
  ];

  it('changes when a path param changes so the same view is rebuilt', () => {
    const a = viewKey(matchRoute('/decks/42', testRoutes));
    const b = viewKey(matchRoute('/decks/43', testRoutes));
    expect(a).not.toBe(b);
  });

  it('changes when the note id changes even within the same deck', () => {
    const a = viewKey(matchRoute('/decks/42/notes/1', testRoutes));
    const b = viewKey(matchRoute('/decks/42/notes/2', testRoutes));
    expect(a).not.toBe(b);
  });

  it('changes when the query changes so a URL-scoped view reloads (review deck scope)', () => {
    const a = viewKey(matchRoute('/review?deck=A', testRoutes));
    const b = viewKey(matchRoute('/review?deck=B', testRoutes));
    expect(a).not.toBe(b);
  });

  it('treats repeated query keys as part of the identity', () => {
    // The parsed query keeps only the last deck; only the raw search string can tell these two scopes apart.
    const two = viewKey(matchRoute('/review?deck=A&deck=B', testRoutes));
    const one = viewKey(matchRoute('/review?deck=B', testRoutes));
    expect(two).not.toBe(one);
    // Two spellings of the same scope must not trigger a redundant rebuild.
    const reordered = viewKey(matchRoute('/review?deck=B&deck=A', testRoutes));
    expect(two).toBe(reordered);
  });

  it('is stable for the same route and gives the not-found view its own key', () => {
    expect(viewKey(matchRoute('/decks/42', testRoutes))).toBe(viewKey(matchRoute('/decks/42', testRoutes)));
    expect(viewKey(matchRoute('/missing', testRoutes))).toBe('not-found:/missing|p=|q=');
  });
});
