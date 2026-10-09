<script lang="ts">
  import { Globe } from '@lucide/svelte';
  import { localeStore, t } from '../i18n';
  import Select from './ui/Select.svelte';
  import { switchLocale } from './language-switch';

  /**
   * 未登录页头的语言切换器；切换逻辑（落点、回滚）见 language-switch.ts。
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
    failed = false;
    saving = true;
    try {
      failed = !(await switchLocale(value));
    } finally {
      saving = false;
    }
  }
</script>

<div
  class="inline-flex h-8 items-center gap-1.5 rounded-md border bg-background px-2 transition-colors {failed
    ? 'border-destructive/50'
    : 'border-input'}"
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

