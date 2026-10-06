<script lang="ts">
  import { t } from '../i18n';
  import { navigate } from '../router';
  import { login } from '../auth';
  import { getApiErrorMessageKey, ApiClientError } from '../api';

  let username = $state('');
  let password = $state('');
  let loading = $state(false);
  let errorKey = $state<string | null>(null);

  async function handleSubmit(e: SubmitEvent): Promise<void> {
    e.preventDefault();
    if (!username.trim() || !password) {
      return;
    }
    loading = true;
    errorKey = null;

    try {
      const res = await login(username.trim(), password);
      if (res.requires_totp) {
        // 第一因素通过、需要第二因素：进入 SPA 第二步（协议 GET/POST /api/v1/auth/totp）。
        // 第二步凭据是服务端在本次响应里下发的 HttpOnly cookie，因此必须立刻导航过去。
        navigate('/login/totp');
      } else if (res.authenticated) {
        navigate('/');
      }
    } catch (err) {
      if (err instanceof ApiClientError) {
        errorKey = getApiErrorMessageKey(err);
      } else {
        errorKey = 'error.unknown';
      }
    } finally {
      loading = false;
    }
  }
</script>

<div class="py-12 max-w-md mx-auto px-4">
  <div class="card-elevated p-8 rounded-xl">
    <div class="mb-6 text-center">
      <h1 class="text-2xl font-bold tracking-tight text-zinc-900 dark:text-zinc-100">
        {$t('auth.login.heading')}
      </h1>
    </div>

    {#if errorKey}
      <div
        data-testid="login-error"
        class="mb-6 p-4 rounded-lg bg-rose-50 dark:bg-rose-950/40 border border-rose-200 dark:border-rose-800/60 text-rose-700 dark:text-rose-300 text-sm flex items-center space-x-2"
      >
        <svg class="w-5 h-5 flex-shrink-0 text-rose-500" fill="none" viewBox="0 0 24 24" stroke="currentColor">
          <path stroke-linecap="round" stroke-linejoin="round" stroke-width="2" d="M12 8v4m0 4h.01M21 12a9 9 0 11-18 0 9 9 0 0118 0z" />
        </svg>
        <span>{$t(errorKey)}</span>
      </div>
    {/if}

    <form onsubmit={handleSubmit} class="space-y-4">
      <div>
        <label for="login-username" class="block text-sm font-medium text-zinc-700 dark:text-zinc-300 mb-1.5">
          {$t('auth.field.username')}
        </label>
        <input
          id="login-username"
          name="username"
          type="text"
          autocomplete="username"
          required
          bind:value={username}
          disabled={loading}
          class="w-full px-3.5 py-2 rounded-lg border border-zinc-300 dark:border-zinc-700 bg-white dark:bg-zinc-900 text-zinc-900 dark:text-zinc-100 placeholder-zinc-400 focus:outline-none focus:ring-2 focus:ring-zinc-900 dark:focus:ring-zinc-100 transition-colors disabled:opacity-50 text-sm"
        />
      </div>

      <div>
        <label for="login-password" class="block text-sm font-medium text-zinc-700 dark:text-zinc-300 mb-1.5">
          {$t('auth.field.password')}
        </label>
        <input
          id="login-password"
          name="password"
          type="password"
          autocomplete="current-password"
          required
          bind:value={password}
          disabled={loading}
          class="w-full px-3.5 py-2 rounded-lg border border-zinc-300 dark:border-zinc-700 bg-white dark:bg-zinc-900 text-zinc-900 dark:text-zinc-100 placeholder-zinc-400 focus:outline-none focus:ring-2 focus:ring-zinc-900 dark:focus:ring-zinc-100 transition-colors disabled:opacity-50 text-sm"
        />
      </div>

      <div class="pt-2">
        <button
          type="submit"
          disabled={loading || !username.trim() || !password}
          class="w-full py-2.5 px-4 rounded-lg bg-zinc-900 dark:bg-zinc-100 text-white dark:text-zinc-900 font-semibold text-sm hover:bg-zinc-800 dark:hover:bg-zinc-200 transition-colors disabled:opacity-50 btn-press cursor-pointer flex items-center justify-center space-x-2"
        >
          {#if loading}
            <div class="w-4 h-4 border-2 border-current border-t-transparent rounded-full animate-spin" aria-hidden="true"></div>
            <span>{$t('auth.login.submitting')}</span>
          {:else}
            <span>{$t('auth.login.submit')}</span>
          {/if}
        </button>
      </div>
    </form>
  </div>
</div>
