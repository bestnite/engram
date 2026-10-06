<script lang="ts">
  import { onMount } from 'svelte';
  import { t } from '../i18n';
  import { apiClient, ApiClientError } from '../api';
  import type { Deck } from '../api';

  // 视图响应式状态定义（Svelte 5 runes）
  let loading = $state(true);
  let error = $state<ApiClientError | Error | null>(null);
  let decks = $state<Deck[]>([]);

  /**
   * 请求后端卡组列表（GET /api/v1/decks）
   * DESIGN.md §7.3、§8.3：同源会话鉴权，错误状态由前端多语言文案承接
   */
  async function fetchDecks(): Promise<void> {
    loading = true;
    error = null;
    try {
      const res = await apiClient.getDecks();
      decks = res.decks;
    } catch (err) {
      error = err instanceof Error ? err : new Error(String(err));
    } finally {
      loading = false;
    }
  }

  onMount(() => {
    fetchDecks();
  });
</script>

<div class="py-10 max-w-4xl mx-auto px-4">
  <div class="card-elevated p-8 rounded-xl">
    <div class="flex items-center justify-between mb-6">
      <h1 class="text-2xl font-bold tracking-tight text-zinc-900 dark:text-zinc-100">
        {$t('decks.title')}
      </h1>
      {#if !loading && !error && decks.length > 0}
        <button
          type="button"
          class="text-xs px-2.5 py-1.5 rounded-md font-medium text-zinc-600 dark:text-zinc-400 hover:text-zinc-900 dark:hover:text-zinc-100 hover:bg-zinc-100 dark:hover:bg-zinc-800 transition-colors btn-press cursor-pointer"
          onclick={fetchDecks}
        >
          {$t('decks.retry')}
        </button>
      {/if}
    </div>

    {#if loading}
      <div data-testid="decks-loading" class="py-12 text-center text-zinc-500 dark:text-zinc-400">
        <div class="inline-block animate-spin w-6 h-6 border-2 border-current border-t-transparent rounded-full mb-3" aria-hidden="true"></div>
        <p class="text-sm">{$t('decks.loading')}</p>
      </div>
    {:else if error}
      <div
        data-testid={error instanceof ApiClientError && error.isUnauthorized ? 'decks-unauthorized' : 'decks-failed'}
        class="py-10 text-center"
      >
        <div class="inline-flex items-center justify-center w-12 h-12 rounded-full bg-rose-100 dark:bg-rose-950/50 text-rose-600 dark:text-rose-400 mb-3">
          <svg class="w-6 h-6" fill="none" viewBox="0 0 24 24" stroke="currentColor">
            <path stroke-linecap="round" stroke-linejoin="round" stroke-width="2" d="M12 9v2m0 4h.01m-6.938 4h13.856c1.54 0 2.502-1.667 1.732-3L13.732 4c-.77-1.333-2.694-1.333-3.464 0L3.34 16c-.77 1.333.192 3 1.732 3z" />
          </svg>
        </div>
        <p class="text-base font-medium text-zinc-900 dark:text-zinc-100 mb-2">
          {#if error instanceof ApiClientError && error.isUnauthorized}
            {$t('decks.unauthorized')}
          {:else}
            {$t('decks.failed')}
          {/if}
        </p>
        <div class="mt-4">
          <button
            data-testid="decks-retry"
            type="button"
            class="px-4 py-2 text-sm font-medium rounded-lg bg-zinc-900 text-white dark:bg-zinc-100 dark:text-zinc-900 hover:bg-zinc-800 dark:hover:bg-zinc-200 transition-colors btn-press cursor-pointer"
            onclick={fetchDecks}
          >
            {$t('decks.retry')}
          </button>
        </div>
      </div>
    {:else if decks.length === 0}
      <div data-testid="decks-empty" class="py-12 text-center text-zinc-500 dark:text-zinc-400">
        <p class="text-base font-medium text-zinc-700 dark:text-zinc-300 mb-1">
          {$t('decks.empty')}
        </p>
      </div>
    {:else}
      <div data-testid="decks-list" class="grid grid-cols-1 sm:grid-cols-2 gap-4">
        {#each decks as deck (deck.id)}
          <div class="card-subtle p-5 rounded-lg flex flex-col justify-between hover:border-zinc-300 dark:hover:border-zinc-700 transition-colors">
            <div>
              <div class="flex items-start justify-between gap-2 mb-2">
                <a
                  href="/decks/{deck.id}"
                  data-testid="deck-name-link-{deck.id}"
                  class="text-base font-semibold text-zinc-900 dark:text-zinc-100 hover:text-blue-600 dark:hover:text-blue-400 hover:underline transition-colors"
                >
                  {deck.name}
                </a>
                {#if deck.visibility}
                  <span class="text-xs px-2 py-0.5 rounded bg-zinc-100 dark:bg-zinc-800 text-zinc-600 dark:text-zinc-400 font-mono">
                    {deck.visibility}
                  </span>
                {/if}
              </div>
              {#if deck.description}
                <p class="text-sm text-zinc-600 dark:text-zinc-400 line-clamp-2 mb-3">
                  {deck.description}
                </p>
              {/if}
            </div>
            <div class="pt-3 border-t border-zinc-200/60 dark:border-zinc-800/60 text-xs text-zinc-400 dark:text-zinc-500 flex items-center justify-between">
              <span>{deck.new_per_day} / {deck.reviews_per_day}</span>
              <a
                href="/decks/{deck.id}"
                data-testid="deck-notes-link-{deck.id}"
                class="font-medium text-zinc-600 dark:text-zinc-400 hover:text-zinc-900 dark:hover:text-zinc-100 hover:underline transition-colors"
              >
                {$t('decks.view_notes')} &rarr;
              </a>
            </div>
          </div>
        {/each}
      </div>
    {/if}
  </div>
</div>
