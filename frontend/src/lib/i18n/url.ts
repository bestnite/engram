/**
 * 匿名访客的语言覆盖参数。
 *
 * 服务端对每个请求按「?lang > 已登录用户设置 > Accept-Language > 站点默认」解析语言
 * （internal/web/middleware.go 的语言中间件），入口 `<html lang>` 也按同一优先级逐请求重写。
 * 所以 `?lang=<code>` 是规范里唯一给匿名访客准备的覆盖通道：不写库、随地址传播。
 *
 * 已登录用户不走这里——他的语言在 /settings 里落库（PATCH /api/v1/profile），
 * 让 URL 长期压过账号设置只会让两处口径打架。
 */
export const LANGUAGE_PARAM = 'lang';

/**
 * 读地址上的语言覆盖参数；不在浏览器环境或没有该参数时返回 null。
 * search 可显式传入，便于测试与在同一次跳转里对目标地址求值。
 */
export function readLanguageFromURL(
  search: string = typeof window === 'undefined' ? '' : window.location.search
): string | null {
  const value = new URLSearchParams(search).get(LANGUAGE_PARAM);
  if (value === null || value.trim() === '') return null;
  return value;
}

/**
 * 把语言写进当前地址（只换查询串，不触发路由）。
 * 用 replaceState 而不是 pushState：语言是显示偏好，不该在浏览器前进后退里多出一层。
 */
export function setLanguageInURL(code: string): void {
  if (typeof window === 'undefined') return;
  const url = new URL(window.location.href);
  url.searchParams.set(LANGUAGE_PARAM, code);
  window.history.replaceState({}, '', url);
}
