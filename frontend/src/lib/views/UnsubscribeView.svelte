<script lang="ts">
  import { onMount } from 'svelte';
  import { t } from '../i18n';
  import { apiClient, ApiClientError } from '../api';

  // 一键退订的 SPA 视图（DESIGN.md §4.7、§8.1）：服务端 GET /unsubscribe 只返回应用壳并下发
  // 会话前双提交 cookie，确认页由本视图渲染。令牌由视图读取 ?token= 后先经
  // GET /api/v1/unsubscribe 读取指名的类型（不消费），用户确认后再经 POST /api/v1/unsubscribe
  // 消费（一次性）。/spa/unsubscribe 保留为迁移期别名，指向同一视图。
  const token =
    typeof window !== 'undefined'
      ? (new URLSearchParams(window.location.search).get('token') ?? '')
      : '';

  let status = $state<'loading' | 'ready' | 'confirming' | 'done' | 'error'>('loading');
  let typeCode = $state('');
  let errorKey = $state('unsubscribe.error.invalid');

  // errorKeyFor 把服务端稳定错误 code 映射到本地化 key；未知 code 回落到通用失败提示。
  // 前端不解析后端英文 message（DESIGN.md §8.3），也不把任何后端文案渲染成 HTML。
  function errorKeyFor(err: unknown): string {
    const code = err instanceof ApiClientError ? err.code : '';
    switch (code) {
      case 'token_expired':
        return 'unsubscribe.error.expired';
      case 'token_used':
        return 'unsubscribe.error.used';
      case 'token_invalid':
      case 'unsubscribe_type_invalid':
        return 'unsubscribe.error.invalid';
      case 'rate_limited':
        return 'error.rate_limited';
      case 'network_error':
        return 'error.network';
      default:
        return 'error.unknown';
    }
  }

  onMount(async () => {
    if (!token) {
      status = 'error';
      errorKey = 'unsubscribe.error.invalid';
      return;
    }
    try {
      const res = await apiClient.readUnsubscribe(token);
      typeCode = res.type;
      status = 'ready';
    } catch (err) {
      status = 'error';
      errorKey = errorKeyFor(err);
    }
  });

  async function confirm(): Promise<void> {
    status = 'confirming';
    try {
      const res = await apiClient.confirmUnsubscribe(token);
      typeCode = res.type;
      status = 'done';
    } catch (err) {
      status = 'error';
      errorKey = errorKeyFor(err);
    }
  }
</script>

<div class="py-12 max-w-md mx-auto px-4">
  <div class="card-elevated p-8 rounded-xl text-center">
    <h1 class="mb-4 text-2xl font-bold tracking-tight text-zinc-900 dark:text-zinc-100">
      {$t('unsubscribe.heading')}
    </h1>

    {#if status === 'loading'}
      <p data-testid="unsubscribe-loading" class="text-sm text-zinc-600 dark:text-zinc-400">{$t('unsubscribe.loading')}</p>
    {:else if status === 'ready' || status === 'confirming'}
      <p class="text-sm text-zinc-600 dark:text-zinc-400">{$t('unsubscribe.intro', { type: $t('settings.notifications.type.' + typeCode) })}</p>
      <button
        data-testid="unsubscribe-confirm"
        type="button"
        onclick={confirm}
        disabled={status === 'confirming'}
        class="mt-6 w-full py-2.5 px-4 rounded-lg bg-zinc-900 dark:bg-zinc-100 text-white dark:text-zinc-900 font-semibold text-sm hover:bg-zinc-800 dark:hover:bg-zinc-200 transition-colors disabled:opacity-50 btn-press cursor-pointer"
      >
        {status === 'confirming' ? $t('unsubscribe.confirming') : $t('unsubscribe.submit')}
      </button>
    {:else if status === 'done'}
      <p data-testid="unsubscribe-done" class="text-sm text-emerald-600 dark:text-emerald-400">{$t('unsubscribe.done', { type: $t('settings.notifications.type.' + typeCode) })}</p>
    {:else}
      <p data-testid="unsubscribe-error" class="text-sm text-rose-600 dark:text-rose-400">{$t(errorKey)}</p>
    {/if}

    <div class="mt-6 text-sm">
      <a href="/settings/notifications" class="text-zinc-600 dark:text-zinc-400 hover:text-zinc-900 dark:hover:text-zinc-100 transition-colors">
        {$t('unsubscribe.back')}
      </a>
    </div>
  </div>
</div>
