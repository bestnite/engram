import { tick } from 'svelte';
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
        search: queryString,
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
          search: queryString,
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
    search: queryString,
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
  commitRoute(matchRoute(target), !replace);
}

/**
 * 把新路由写进 store，并在浏览器支持时包进一次 View Transition（app.css 定义了 140ms 淡入淡出）。
 *
 * 不支持的浏览器、非浏览器环境（测试）与「减少动态效果」的用户都同步写入，行为与改版前一致。
 * 过渡回调等 tick()：Svelte 在微任务里才把 store 变化刷到 DOM，不等的话新快照拍到的还是旧页面。
 * scrollTop 为真时回到页首：pushState 不会滚动，进入新页面却停在上一页的滚动位置，看起来像跳到了页中间。
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
 * 视图重建键：路径模式 + 路径参数 + 查询串（按段排序，保证稳定）。
 *
 * 应用按「页面组件类型」渲染视图，同一组件的不同实体（卡组 A→B、笔记 A→B）若共用实例，
 * 只在挂载时取数的视图会保留上一实体的数据。把这个键交给 `{#key}` 即建立按路由的重建边界：
 * 路径模式、路径参数或查询串一变，组件销毁重建。
 *
 * 查询串必须参与：复习页的卡组范围就写在地址上（`/review?deck=A`），换范围却不重建的话，
 * 页面会继续显示上一个范围的卡、也不会重新取队列。用原始查询串（`search`）而不是解析后的
 * `query`：后者对重复键只留最后一个，`?deck=A&deck=B` 与 `?deck=B` 会被判成同一个键。
 * 排序只为让「同一次范围的不同书写顺序」不触发多余重建。
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
 * 初始化浏览器路由事件监听（popstate 与链接代理）
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
