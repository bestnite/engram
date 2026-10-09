import { writable, get } from 'svelte/store';
import type { RouteDefinition, RouteMatch } from './types';
import { routes } from './routes';
import { authStore } from '../auth';
import { LANGUAGE_PARAM, readLanguageFromURL } from '../i18n/url';

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
 *
 * 跳转要带上未登录访客的语言覆盖参数（见 withLanguage）：站内链接统一走这里，
 * 而 pushState/replaceState 会把整个 URL 换掉，不带过去就等于把语言选择丢掉。
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
  routeStore.set(matchRoute(target));
}

/**
 * 未登录访客的语言选择只存在于地址上（?lang=<code>），把当前地址上的它并到跳转目标上——
 * 不带过去的话，跳一次页面再刷新就退回浏览器语言。
 *
 * 已登录用户不保留：他的语言在 /settings 里落库，让 URL 长期压过账号设置只会让
 * 「页头与设置页不一致」这个已修过的缺陷换个形态回来。登录成功后 navigate('/')
 * 会把查询串整体换掉，所以匿名期留下的 ?lang 不会跟着进登录态。
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
 * 视图重建键：路径模式 + 路径参数（不含查询串）。
 *
 * 应用按「页面组件类型」渲染视图，同一组件的不同实体（卡组 A→B、笔记 A→B）若共用实例，
 * 只在挂载时取数的视图会保留上一实体的数据。把这个键交给 `{#key}` 即建立按路由的重建边界：
 * 路径模式或路径参数一变，组件销毁重建。查询串刻意不参与——筛选与分页是组件内状态，
 * 改它们不该重建页面。
 */
export function viewKey(match: RouteMatch): string {
  if (!match.route) return `not-found|${match.path}`;
  const params = Object.keys(match.params)
    .sort()
    .map((key) => `${key}=${match.params[key]}`)
    .join('&');
  return `${match.route.path}|${params}`;
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
