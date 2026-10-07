<script lang="ts">
  import { onMount } from 'svelte';
  import { t } from '../i18n';
  import { apiClient, ApiClientError } from '../api';
  import type { ShareResponse } from '../api';
  import { routeStore } from '../router';

  // 公开只读分享浏览页（服务端 GET /s/:token 切壳后由客户端路由渲染此页）。
  // 卡片正反面一律是服务端清洗后的 HTML，这里只把清洗结果作为标签注入，绝不把 fields 原文当 Markdown 渲染。
  // 媒体（<img src="/media/<sha>">）的可见性仍由服务端判定：只有登录且打开过该卡组的会话才放行。
  const token = $derived($routeStore.params.token ?? '');

  let status = $state<'loading' | 'password' | 'content' | 'error'>('loading');
  let share = $state<ShareResponse | null>(null);
  let password = $state('');
  let unlocking = $state(false);
  let errorKey = $state<string | null>(null);

  // 把分享接口的稳定错误 code 映射到 share.* 语言包键；绝不回显后端英文 message。
  function mapShareError(err: unknown): string {
    if (err instanceof ApiClientError) {
      if (err.code === 'share_password_invalid') return 'share.error_password';
      if (err.isNotFound) return 'share.error_not_found';
      if (err.isNetworkError) return 'error.network';
    }
    return 'share.error_not_found';
  }

  async function loadShare(): Promise<void> {
    status = 'loading';
    try {
      const res = await apiClient.getShare(token);
      share = res;
      status = res.password_required ? 'password' : 'content';
    } catch (err) {
      errorKey = mapShareError(err);
      status = 'error';
    }
  }

  async function handleUnlock(e: SubmitEvent): Promise<void> {
    e.preventDefault();
    unlocking = true;
    errorKey = null;
    try {
      const res = await apiClient.unlockShare(token, password);
      share = res;
      status = 'content';
    } catch (err) {
      errorKey = mapShareError(err);
    } finally {
      unlocking = false;
    }
  }

  onMount(loadShare);
</script>

<div class="py-8 max-w-3xl mx-auto px-4">
  <div class="mb-6">
    <h1 class="text-2xl font-bold tracking-tight text-zinc-900 dark:text-zinc-100">
      {$t('share.browse.heading')}
    </h1>
    {#if share}
      <p data-testid="share-deck-name" class="mt-1 text-sm text-zinc-500 dark:text-zinc-400">{share.deck_name}</p>
    {/if}
  </div>

  {#if status === 'loading'}
    <p data-testid="share-loading" class="text-sm text-zinc-500 dark:text-zinc-400">{$t('share.browse.loading')}</p>
  {:else if status === 'error'}
    <div
      data-testid="share-error"
      class="p-4 rounded-lg bg-rose-50 dark:bg-rose-950/40 border border-rose-200 dark:border-rose-800/60 text-rose-700 dark:text-rose-300 text-sm"
    >
      <span>{errorKey ? $t(errorKey) : $t('share.error_not_found')}</span>
    </div>
  {:else if status === 'password'}
    <div class="card-elevated p-6 rounded-xl max-w-md">
      <p class="mb-4 text-sm text-zinc-600 dark:text-zinc-400">{$t('share.password.intro')}</p>
      <form onsubmit={handleUnlock} class="space-y-4">
        <div>
          <label for="share-password" class="block text-sm font-medium text-zinc-700 dark:text-zinc-300 mb-1.5">
            {$t('share.password.label')}
          </label>
          <input
            id="share-password"
            name="password"
            type="password"
            autocomplete="current-password"
            required
            bind:value={password}
            disabled={unlocking}
            placeholder={$t('share.password.placeholder')}
            class="w-full px-3.5 py-2 rounded-lg border border-zinc-300 dark:border-zinc-700 bg-white dark:bg-zinc-900 text-zinc-900 dark:text-zinc-100 focus:outline-none focus:ring-2 focus:ring-zinc-900 dark:focus:ring-zinc-100 transition-colors disabled:opacity-50 text-sm"
          />
        </div>
        {#if errorKey}
          <p data-testid="share-password-error" class="text-sm text-rose-600 dark:text-rose-400">{$t(errorKey)}</p>
        {/if}
        <button
          type="submit"
          disabled={unlocking || !password}
          class="w-full py-2.5 px-4 rounded-lg bg-zinc-900 dark:bg-zinc-100 text-white dark:text-zinc-900 font-semibold text-sm hover:bg-zinc-800 dark:hover:bg-zinc-200 transition-colors disabled:opacity-50 btn-press cursor-pointer"
        >
          {unlocking ? $t('share.password.submitting') : $t('share.password.submit')}
        </button>
      </form>
    </div>
  {:else if share}
    <p class="mb-6 text-sm text-zinc-600 dark:text-zinc-400">{$t('share.browse.intro')}</p>

    {#if share.notes.length === 0}
      <p data-testid="share-empty" class="text-sm text-zinc-500 dark:text-zinc-400">{$t('share.browse.empty')}</p>
    {:else}
      <ul class="space-y-4">
        {#each share.notes as note, i}
          <li data-testid="share-note" class="card-elevated p-5 rounded-xl">
            <div class="text-xs font-semibold uppercase tracking-wider text-zinc-400 dark:text-zinc-500">{$t('share.browse.front')}</div>
            <div class="mt-2 text-base text-zinc-950 dark:text-zinc-100 leading-relaxed">{@html note.front_html}</div>
            <hr class="my-4 border-zinc-200 dark:border-zinc-800" />
            <div class="text-xs font-semibold uppercase tracking-wider text-zinc-400 dark:text-zinc-500">{$t('share.browse.back')}</div>
            <div class="mt-2 text-base text-zinc-700 dark:text-zinc-300 leading-relaxed">{@html note.back_html}</div>
            <span class="sr-only">{i + 1}</span>
          </li>
        {/each}
      </ul>
    {/if}

    <div class="mt-8 text-center text-sm">
      <a href="/spa/login" class="text-zinc-600 dark:text-zinc-400 hover:text-zinc-900 dark:hover:text-zinc-100 transition-colors">
        {$t('share.browse.login')}
      </a>
    </div>
  {/if}
</div>
