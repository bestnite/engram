import { writable, get } from 'svelte/store';
import type { RouteDefinition, RouteMatch } from './types';
import { routes } from './routes';

/**
 * 解析查询参数字符串为键值对象
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
 * 匹配路径与路由规则
 */
export function matchRoute(pathname: string, routeList: RouteDefinition[] = routes): RouteMatch {
  const [pathOnly = '', queryString = ''] = pathname.split('?');
  const cleanPath = pathOnly === '' ? '/' : pathOnly;
  const query = parseQuery(queryString);

  for (const route of routeList) {
    // 精确匹配
    if (route.path === cleanPath) {
      return {
        path: cleanPath,
        params: {},
        query,
        route,
      };
    }

    // 动态参数匹配，例如 /decks/:id
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
          route,
        };
      }
    }
  }

  // 未匹配到路由（404）
  return {
    path: cleanPath,
    params: {},
    query,
    route: null,
  };
}

/**
 * 获取当前初始路径
 */
function getInitialPath(): string {
  if (typeof window !== 'undefined' && window.location) {
    return window.location.pathname + window.location.search;
  }
  return '/';
}

/**
 * 当前路由匹配信息 Store
 */
export const routeStore = writable<RouteMatch>(matchRoute(getInitialPath()));

/**
 * 客户端路由跳转
 */
export function navigate(to: string, replace = false): void {
  if (typeof window !== 'undefined') {
    if (replace) {
      window.history.replaceState({}, '', to);
    } else {
      window.history.pushState({}, '', to);
    }
  }
  routeStore.set(matchRoute(to));
}

/**
 * 初始化浏览器路由事件监听（popstate 与链接代理）
 */
export function initRouter(): () => void {
  if (typeof window === 'undefined') return () => {};

  const handlePopState = () => {
    routeStore.set(matchRoute(window.location.pathname + window.location.search));
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

export { get };
export * from './types';
export * from './routes';
