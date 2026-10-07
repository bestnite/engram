<script lang="ts">
  import { Globe } from '@lucide/svelte';
  import { localeStore, setLocale, t, type SupportedLocale } from '../i18n';
  import { setLanguageInURL } from '../i18n/url';
  import Select from './ui/Select.svelte';

  /**
   * 页头语言切换器，只渲染给未登录访客（由 NavHeader 判权限）。
   *
   * 匿名访客没有可写库的账号，所以选择写进地址的 `?lang=<code>`：服务端对每个请求都按
   * 「?lang 优先」解析语言，刷新与把地址发给别人都能保留，且不落库。已登录用户的语言
   * 入口在 /settings（随资料表单落库）——此前两处并存，页头切完刷新就回退，被当成缺陷报过。
   *
   * 下拉本体是 ui/Select（bits-ui），不再用原生 select 元素：原生控件在深浅两套主题下
   * 外观由系统决定，与页头其它控件排在一起时高度与配色都对不齐。
   */
  const options = $derived([
    { value: 'zh-CN', label: $t('language.zh-CN') },
    { value: 'en', label: $t('language.en') },
  ]);

  function handleSelect(value: string) {
    setLocale(value as SupportedLocale);
    setLanguageInURL(value);
  }
</script>

<div class="inline-flex h-8.5 items-center gap-1.5 rounded-lg border border-zinc-200 bg-white px-2 transition-colors dark:border-zinc-700 dark:bg-zinc-800">
  <Globe class="size-3.5 shrink-0 opacity-70" aria-hidden="true" />
  <Select
    value={$localeStore}
    {options}
    onValueChange={handleSelect}
    ariaLabel={$t('language.label')}
    testId="language-select"
    size="sm"
    class="w-20 border-0 bg-transparent px-0 hover:bg-transparent dark:bg-transparent dark:hover:bg-transparent"
  />
</div>
