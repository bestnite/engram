<script lang="ts">
  import { Globe } from '@lucide/svelte';
  import { authStore } from '../auth';
  import { apiClient } from '../api';
  import { isSupportedLocale, localeStore, setLocale, t } from '../i18n';
  import { clearLanguageInURL, setLanguageInURL } from '../i18n/url';
  import Select from './ui/Select.svelte';

  /**
   * 页头语言切换器。两种身份、两种落点，界面都立即生效：
   *
   * - 未登录：没有账号可写，选择写进地址的 `?lang=<code>`（服务端对每个请求都按「?lang 优先」
   *   解析语言），刷新与把地址发给别人都保留，且不落库；
   * - 已登录：`PATCH /api/v1/settings/locale` 写 `users.locale`，与 /settings 的语言字段写的是
   *   同一列；同时把地址上残留的 `?lang=` 清掉——它在服务端解析里优先级最高，留着会压过刚写好的
   *   账号设置（表现为「切完语言，刷新又变回去」）。
   *
   * 失败时不留下「界面变了、库里没变」的错觉：回滚到切换前的语言，并给出一行可读的提示。
   *
   * 下拉本体是 ui/Select（bits-ui），不再用原生 select 元素：原生控件在深浅两套主题下
   * 外观由系统决定，与页头其它控件排在一起时高度与配色都对不齐。
   */
  const options = $derived([
    { value: 'zh-CN', label: $t('language.zh-CN') },
    { value: 'en', label: $t('language.en') },
  ]);

  let saving = $state(false);
  let failed = $state(false);

  async function handleSelect(value: string) {
    if (!isSupportedLocale(value)) return;
    const previous = $localeStore;
    if (value === previous) return;
    failed = false;
    setLocale(value);

    if (!$authStore.authenticated) {
      setLanguageInURL(value);
      return;
    }

    clearLanguageInURL();
    saving = true;
    try {
      await apiClient.updateLocale(value);
    } catch {
      setLocale(previous);
      failed = true;
    } finally {
      saving = false;
    }
  }
</script>

<div
  class="inline-flex h-8.5 items-center gap-1.5 rounded-lg border bg-white px-2 transition-colors dark:bg-zinc-800 {failed
    ? 'border-rose-300 dark:border-rose-800'
    : 'border-zinc-200 dark:border-zinc-700'}"
  title={failed ? $t('language.save_failed') : undefined}
>
  <Globe class="size-3.5 shrink-0 opacity-70" aria-hidden="true" />
  <Select
    value={$localeStore}
    {options}
    onValueChange={handleSelect}
    disabled={saving}
    ariaLabel={$t('language.label')}
    testId="language-select"
    size="sm"
    class="w-20 border-0 bg-transparent px-0 hover:bg-transparent dark:bg-transparent dark:hover:bg-transparent"
  />
  {#if failed}
    <p role="alert" class="sr-only">{$t('language.save_failed')}</p>
  {/if}
</div>

