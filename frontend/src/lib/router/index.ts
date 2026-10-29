import { tick } from 'svelte';
import { writable, get } from 'svelte/store';
import type { RouteDefinition, RouteMatch } from './types';
import { routes } from './routes';
import { authStore } from '../auth';
import { LANGUAGE_PARAM, readLanguageFromURL } from '../i18n/url';

/**
 * Parse a query string into a key/value object
 */
export function parseQuery(queryString: string): Record<string, string> {
  const query: Record<string, string> = {};
  if (!queryString) return query;

  const search = queryString.startsWith('?') ? queryString.slice(1) : queryString;
  const pairs = search.split('&');
  for (const pair of pairs) {
    if (!pair) continue;
    const [key, value] = pair.split('=');
    if (key) {
      query[decodeURIComponent(key)] = value ? decodeURIComponent(value) : '';
    }
  }
  return query;
}

/**
 * Match a path against the route rules
 */
export function matchRoute(pathname: string, routeList: RouteDefinition[] = routes): RouteMatch {
  const [pathOnly = '', queryString = ''] = pathname.split('?');
  const cleanPath = pathOnly === '' ? '/' : pathOnly;
  const query = parseQuery(queryString);

  for (const route of routeList) {
    // Exact match
    if (route.path === cleanPath) {
      return {
        path: cleanPath,
        params: {},
        query,
        search: queryString,
        route,
      };
    }

    // Dynamic parameter match, e.g. /decks/:id
    const routeParts = route.path.split('/').filter(Boolean);
    const pathParts = cleanPath.split('/').filter(Boolean);

    if (routeParts.length === pathParts.length) {
      const params: Record<string, string> = {};
      let matched = true;

      for (let i = 0; i < routeParts.length; i++) {
        const rPart = routeParts[i];
        const pPart = pathParts[i];
        if (!rPart || !pPart) {
          matched = false;
          break;
        }

        if (rPart.startsWith(':')) {
          const paramName = rPart.slice(1);
          params[paramName] = decodeURIComponent(pPart);
        } else if (rPart !== pPart) {
          matched = false;
          break;
        }
      }

      if (matched) {
        return {
          path: cleanPath,
          params,
          query,
          search: queryString,
          route,
        };
      }
    }
  }

  // No route matched (404)
  return {
    path: cleanPath,
    params: {},
    query,
    search: queryString,
    route: null,
  };
}

/**
 * Get the current initial path
 */
function getInitialPath(): string {
  if (typeof window !== 'undefined' && window.location) {
    return window.location.pathname + window.location.search;
  }
  return '/';
}

/**
 * Store holding the current route match
 */
export const routeStore = writable<RouteMatch>(matchRoute(getInitialPath()));

/**
 * Client-side route navigation
 *
 * Navigation must carry the language override parameter of anonymous visitors (see withLanguage): all in-app links
 * go through here, and pushState/replaceState swaps the whole URL, so not carrying it over means dropping the language choice.
 */
export function navigate(to: string, replace = false): void {
  const target = withLanguage(to);
  if (typeof window !== 'undefined') {
    if (replace) {
      window.history.replaceState({}, '', target);
    } else {
      window.history.pushState({}, '', target);
    }
  }
  commitRoute(matchRoute(target), !replace);
}

/**
 * Rewrite the address bar in place without a route change.
 *
 * For a view that mirrors its own state (filters, page number) into the query string, so a reload, the
 * browser's back button and a `?return=` link all land on the same state. The store is deliberately left
 * alone: the query string is part of the view rebuild key, and committing it would destroy and recreate
 * the view on every keystroke. The next navigate or popstate reads the address afresh, so nothing keeps
 * the stale store value.
 */
export function replaceAddress(to: string): void {
  if (typeof window === 'undefined') return;
  const target = withLanguage(to);
  if (window.location.pathname + window.location.search === target) return;
  window.history.replaceState(window.history.state, '', target);
}

/**
 * Write the new route into the store, wrapping it in a View Transition when the browser supports it
 * (app.css defines a 140ms fade).
 *
 * Browsers without support, non-browser environments (tests) and users who prefer reduced motion all write
 * synchronously, matching the behavior before the rework.
 * The transition callback awaits tick(): Svelte only flushes store changes to the DOM in a microtask, and without
 * waiting the new snapshot still captures the old page.
 * When scrollTop is true it scrolls back to the top: pushState does not scroll, so entering a new page would stay at
 * the previous page's scroll offset and look like landing in the middle of the page.
 */
function commitRoute(match: RouteMatch, scrollTop: boolean): void {
  const apply = () => {
    routeStore.set(match);
    if (scrollTop && typeof window !== 'undefined' && typeof window.scrollTo === 'function') {
      window.scrollTo(0, 0);
    }
  };
  if (!canAnimateRoute()) {
    apply();
    return;
  }
  (document as Document & { startViewTransition: (cb: () => Promise<void>) => unknown }).startViewTransition(async () => {
    apply();
    await tick();
  });
}

function canAnimateRoute(): boolean {
  if (typeof document === 'undefined' || typeof window === 'undefined') return false;
  if (!('startViewTransition' in document)) return false;
  return !(window.matchMedia && window.matchMedia('(prefers-reduced-motion: reduce)').matches);
}

/**
 * An anonymous visitor's language choice exists only in the address (?lang=<code>), so merge the one from the
 * current address into the navigation target — without carrying it over, one navigation plus a refresh falls
 * back to the browser language.
 *
 * Logged-in users do not keep it: their language is stored in /settings, and letting the URL override the account
 * setting for long would only bring back the already-fixed defect "header and settings page disagree" in another
 * shape. After a successful login navigate('/') replaces the whole query string, so a ?lang left over from the
 * anonymous period does not follow into the logged-in state.
 */
function withLanguage(to: string): string {
  if (typeof window === 'undefined' || get(authStore).authenticated) return to;
  const lang = readLanguageFromURL();
  if (lang === null) return to;

  const [path = '', rest = ''] = to.split('?');
  const [search = '', hash = ''] = rest.split('#');
  const params = new URLSearchParams(search);
  if (params.has(LANGUAGE_PARAM)) return to;
  params.set(LANGUAGE_PARAM, lang);
  return `${path}?${params.toString()}${hash ? `#${hash}` : ''}`;
}

/**
 * View rebuild key: path pattern + path params + query string (segments sorted, for stability).
 *
 * The app renders views by "page component type", so if different entities of the same component (deck A→B,
 * note A→B) share an instance, views that fetch data only on mount keep the previous entity's data. Handing this
 * key to `{#key}` establishes a per-route rebuild boundary: once the path pattern, path params or query string
 * change, the component is destroyed and recreated.
 *
 * The query string must take part: the review page's deck scope is written in the address (`/review?deck=A`), and
 * changing the scope without rebuilding would keep showing the previous scope's cards and not refetch the queue.
 * Use the raw query string (`search`) rather than the parsed `query`: the latter keeps only the last value of a
 * duplicate key, so `?deck=A&deck=B` and `?deck=B` would be judged the same key.
 * Sorting is only there so "the same scope written in a different order" does not trigger needless rebuilds.
 */
export function viewKey(match: RouteMatch): string {
  const route = match.route ? match.route.path : `not-found:${match.path}`;
  const params = Object.keys(match.params)
    .sort()
    .map((key) => `${key}=${match.params[key]}`)
    .join('&');
  const search = match.search
    .split('&')
    .filter(Boolean)
    .sort()
    .join('&');
  return `${route}|p=${params}|q=${search}`;
}

/**
 * Initialize the browser route event listeners (popstate and link proxy)
 */
export function initRouter(): () => void {
  if (typeof window === 'undefined') return () => {};

  const handlePopState = () => {
    commitRoute(matchRoute(window.location.pathname + window.location.search), false);
  };

  const handleClick = (e: MouseEvent) => {
    const target = (e.target as HTMLElement | null)?.closest('a');
    if (!target) return;

    const href = target.getAttribute('href');
    if (
      !href ||
      href.startsWith('http://') ||
      href.startsWith('https://') ||
      href.startsWith('//') ||
      href.startsWith('mailto:') ||
      target.target === '_blank' ||
      e.defaultPrevented ||
      e.button !== 0 ||
      e.metaKey ||
      e.ctrlKey ||
      e.shiftKey ||
      e.altKey
    ) {
      return;
    }

    if (href.startsWith('/')) {
      // Take over only links this router can render. A same-origin link to a server endpoint such as
      // /auth/oidc/start or /api/... must reach the server as a normal navigation; proxying it would
      // match no client route and show the SPA 404 instead of starting the server-side flow.
      if (matchRoute(href).route === null) return;
      e.preventDefault();
      navigate(href);
    }
  };

  window.addEventListener('popstate', handlePopState);
  document.addEventListener('click', handleClick);

  return () => {
    window.removeEventListener('popstate', handlePopState);
    document.removeEventListener('click', handleClick);
  };
}

export * from './types';
export * from './routes';
