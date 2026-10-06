<script lang="ts">
  import { localeStore, setLocale, t, type SupportedLocale } from '../i18n';

  /**
   * 页头语言切换器（DESIGN.md §8.3）
   */
  function handleSelect(event: Event) {
    const target = event.target as HTMLSelectElement;
    const value = target.value as SupportedLocale;
    setLocale(value);
  }
</script>

<div class="relative inline-flex items-center">
  <label for="language-select" class="sr-only">
    {$t('language.label')}
  </label>
  <select
    id="language-select"
    value={$localeStore}
    onchange={handleSelect}
    class="text-xs font-medium bg-transparent border border-zinc-300 dark:border-zinc-700 rounded-md px-2 py-1 text-zinc-700 dark:text-zinc-300 focus:outline-none focus:ring-1 focus:ring-zinc-950 dark:focus:ring-zinc-300 transition-colors cursor-pointer"
  >
    <option value="zh-CN">{$t('language.zh-CN')}</option>
    <option value="en">{$t('language.en')}</option>
  </select>
</div>
