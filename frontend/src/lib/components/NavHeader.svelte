<script lang="ts">
  import { t } from '../i18n';
  import { routeStore } from '../router';
  import LanguageSwitcher from './LanguageSwitcher.svelte';
</script>

<header class="border-b border-zinc-200 dark:border-zinc-800 bg-white/80 dark:bg-zinc-950/80 backdrop-blur-md sticky top-0 z-20">
  <div class="max-w-5xl mx-auto px-4 h-14 flex items-center justify-between">
    <div class="flex items-center space-x-6">
      <a href="/" class="flex items-center space-x-2 text-base font-bold text-zinc-900 dark:text-zinc-100 tracking-tight">
        <svg class="w-5 h-5 text-indigo-600 dark:text-indigo-400" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2">
          <rect x="4" y="4" width="16" height="16" rx="2" />
          <path d="M9 9h6v6H9z" />
        </svg>
        <span>{$t('app.name')}</span>
      </a>

      <nav class="hidden md:flex items-center space-x-1" aria-label="Main Navigation">
        <a
          href="/"
          class="px-3 py-1.5 rounded-md text-sm font-medium transition-colors {$routeStore.path === '/' ? 'text-zinc-900 dark:text-zinc-100 bg-zinc-100 dark:bg-zinc-900 font-semibold' : 'text-zinc-600 hover:text-zinc-900 dark:text-zinc-400 dark:hover:text-zinc-200'}"
        >
          {$t('nav.today')}
        </a>
        <a
          href="/decks"
          class="px-3 py-1.5 rounded-md text-sm font-medium transition-colors {$routeStore.path.startsWith('/decks') ? 'text-zinc-900 dark:text-zinc-100 bg-zinc-100 dark:bg-zinc-900 font-semibold' : 'text-zinc-600 hover:text-zinc-900 dark:text-zinc-400 dark:hover:text-zinc-200'}"
        >
          {$t('nav.decks')}
        </a>
        <a
          href="/stats"
          class="px-3 py-1.5 rounded-md text-sm font-medium transition-colors {$routeStore.path.startsWith('/stats') ? 'text-zinc-900 dark:text-zinc-100 bg-zinc-100 dark:bg-zinc-900 font-semibold' : 'text-zinc-600 hover:text-zinc-900 dark:text-zinc-400 dark:hover:text-zinc-200'}"
        >
          {$t('nav.stats')}
        </a>
        <a
          href="/settings"
          class="px-3 py-1.5 rounded-md text-sm font-medium transition-colors {$routeStore.path.startsWith('/settings') ? 'text-zinc-900 dark:text-zinc-100 bg-zinc-100 dark:bg-zinc-900 font-semibold' : 'text-zinc-600 hover:text-zinc-900 dark:text-zinc-400 dark:hover:text-zinc-200'}"
        >
          {$t('nav.settings')}
        </a>
      </nav>
    </div>

    <div class="flex items-center space-x-3">
      <LanguageSwitcher />
    </div>
  </div>
</header>
