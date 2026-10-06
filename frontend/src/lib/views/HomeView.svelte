<script lang="ts">
  import { t } from '../i18n';
</script>

<div class="py-10 max-w-4xl mx-auto px-4">
  <div class="card-elevated p-8 rounded-xl">
    <div class="flex items-center justify-between mb-4">
      <h1 class="text-2xl font-bold tracking-tight text-zinc-900 dark:text-zinc-100">
        {$t('shell.welcome')}
      </h1>
      <span class="inline-flex items-center px-2.5 py-0.5 rounded-full text-xs font-medium bg-emerald-100 text-emerald-800 dark:bg-emerald-950 dark:text-emerald-300">
        {$t('shell.status')}
      </span>
    </div>
    <p class="text-zinc-600 dark:text-zinc-400 mb-6">
      {$t('shell.subtitle')}
    </p>
    <div class="grid grid-cols-1 sm:grid-cols-3 gap-4 pt-4 border-t border-zinc-200 dark:border-zinc-800">
      <a
        href="/decks"
        class="card-subtle p-4 rounded-lg block hover:border-zinc-300 dark:hover:border-zinc-700 transition-colors btn-press"
      >
        <div class="font-semibold text-zinc-900 dark:text-zinc-100 mb-1">{$t('nav.decks')}</div>
        <div class="text-sm text-zinc-500 dark:text-zinc-400">{$t('nav.decks')}</div>
      </a>
      <a
        href="/stats"
        class="card-subtle p-4 rounded-lg block hover:border-zinc-300 dark:hover:border-zinc-700 transition-colors btn-press"
      >
        <div class="font-semibold text-zinc-900 dark:text-zinc-100 mb-1">{$t('nav.stats')}</div>
        <div class="text-sm text-zinc-500 dark:text-zinc-400">{$t('nav.stats')}</div>
      </a>
      <a
        href="/settings"
        class="card-subtle p-4 rounded-lg block hover:border-zinc-300 dark:hover:border-zinc-700 transition-colors btn-press"
      >
        <div class="font-semibold text-zinc-900 dark:text-zinc-100 mb-1">{$t('nav.settings')}</div>
        <div class="text-sm text-zinc-500 dark:text-zinc-400">{$t('nav.settings')}</div>
      </a>
    </div>
  </div>
</div>
