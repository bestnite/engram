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
  <div class="inline-flex items-center h-8.5 px-2 rounded-lg border border-zinc-200 dark:border-zinc-700 bg-white dark:bg-zinc-800 text-zinc-600 dark:text-zinc-300 transition-colors">
    <svg class="w-3.5 h-3.5 mr-1.5 shrink-0 opacity-70" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round" aria-hidden="true">
      <circle cx="12" cy="12" r="10" />
      <path d="M12 2a14.5 14.5 0 0 0 0 20M12 2a14.5 14.5 0 0 1 0 20M2 12h20" />
    </svg>
    <select
      id="language-select"
      value={$localeStore}
      onchange={handleSelect}
      class="text-xs font-medium bg-transparent text-zinc-700 dark:text-zinc-200 focus:outline-none cursor-pointer pr-1"
    >
      <option value="zh-CN">{$t('language.zh-CN')}</option>
      <option value="en">{$t('language.en')}</option>
    </select>
  </div>
</div>
