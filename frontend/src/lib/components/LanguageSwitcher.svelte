<script lang="ts">
  import { Globe } from '@lucide/svelte';
  import { localeStore, setLocale, t, type SupportedLocale } from '../i18n';
  import Select from './ui/Select.svelte';

  /**
   * 页头语言切换器（DESIGN.md §8.3）。
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
