import { get } from 'svelte/store';
import { authStore } from '../auth';
import { apiClient } from '../api';
import { isSupportedLocale, localeStore, setLocale } from '../i18n';
import { clearLanguageInURL, setLanguageInURL } from '../i18n/url';

/**
 * 切换界面语言。页头的语言下拉与侧边栏账户菜单共用这一份逻辑，两种身份、两种落点：
 *
 * - 未登录：没有账号可写，选择写进地址的 `?lang=<code>`（服务端对每个请求都按「?lang 优先」
 *   解析语言），刷新与把地址发给别人都保留，且不落库；
 * - 已登录：`PATCH /api/v1/settings/locale` 写 `users.locale`，与 /settings 的语言字段写的是
 *   同一列；同时把地址上残留的 `?lang=` 清掉——它在服务端解析里优先级最高，留着会压过刚写好的
 *   账号设置（表现为「切完语言，刷新又变回去」）。
 *
 * 失败时不留下「界面变了、库里没变」的错觉：回滚到切换前的语言并返回 false，由调用方给出提示。
 */
export async function switchLocale(value: string): Promise<boolean> {
  if (!isSupportedLocale(value)) return true;
  const previous = get(localeStore);
  if (value === previous) return true;
  setLocale(value);

  if (!get(authStore).authenticated) {
    setLanguageInURL(value);
    return true;
  }

  clearLanguageInURL();
  try {
    await apiClient.updateLocale(value);
    return true;
  } catch {
    setLocale(previous);
    return false;
  }
}
