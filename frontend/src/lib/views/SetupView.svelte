<script lang="ts">
  import { t } from '../i18n';
  import { navigate } from '../router';
  import { setup } from '../auth';
  import { getApiErrorMessageKey, ApiClientError } from '../api';

  let username = $state('');
  let email = $state('');
  let displayName = $state('');
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
      await setup({
        username: username.trim(),
        email: email.trim(),
        display_name: displayName.trim(),
        password,
      });
      // 引导不建立会话：与 SSR 一致，成功后回到登录页。
      navigate('/spa/login');
    } catch (err) {
      if (err instanceof ApiClientError && err.status === 404) {
        // 已有活跃管理员：引导窗口已关闭（GET /spa/setup 也会 404）。
        errorKey = 'auth.setup.unavailable';
      } else {
        errorKey = err instanceof ApiClientError ? getApiErrorMessageKey(err) : 'error.unknown';
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
        {$t('auth.setup.heading')}
      </h1>
      <p class="mt-2 text-sm text-zinc-600 dark:text-zinc-400">
        {$t('auth.setup.intro')}
      </p>
    </div>

    {#if errorKey}
      <div
        data-testid="setup-error"
        class="mb-6 p-4 rounded-lg bg-rose-50 dark:bg-rose-950/40 border border-rose-200 dark:border-rose-800/60 text-rose-700 dark:text-rose-300 text-sm"
      >
        <span>{$t(errorKey)}</span>
      </div>
    {/if}

    <form onsubmit={handleSubmit} class="space-y-4">
      <div>
        <label for="setup-username" class="block text-sm font-medium text-zinc-700 dark:text-zinc-300 mb-1.5">
          {$t('auth.field.username')}
        </label>
        <input
          id="setup-username"
          name="username"
          type="text"
          autocomplete="username"
          required
          bind:value={username}
          disabled={loading}
          class="w-full px-3.5 py-2 rounded-lg border border-zinc-300 dark:border-zinc-700 bg-white dark:bg-zinc-900 text-zinc-900 dark:text-zinc-100 focus:outline-none focus:ring-2 focus:ring-zinc-900 dark:focus:ring-zinc-100 transition-colors disabled:opacity-50 text-sm"
        />
      </div>

      <div>
        <label for="setup-email" class="block text-sm font-medium text-zinc-700 dark:text-zinc-300 mb-1.5">
          {$t('auth.field.email')}
        </label>
        <input
          id="setup-email"
          name="email"
          type="email"
          autocomplete="email"
          bind:value={email}
          disabled={loading}
          class="w-full px-3.5 py-2 rounded-lg border border-zinc-300 dark:border-zinc-700 bg-white dark:bg-zinc-900 text-zinc-900 dark:text-zinc-100 focus:outline-none focus:ring-2 focus:ring-zinc-900 dark:focus:ring-zinc-100 transition-colors disabled:opacity-50 text-sm"
        />
      </div>

      <div>
        <label for="setup-display-name" class="block text-sm font-medium text-zinc-700 dark:text-zinc-300 mb-1.5">
          {$t('auth.field.display_name')}
        </label>
        <input
          id="setup-display-name"
          name="display_name"
          type="text"
          autocomplete="name"
          bind:value={displayName}
          disabled={loading}
          class="w-full px-3.5 py-2 rounded-lg border border-zinc-300 dark:border-zinc-700 bg-white dark:bg-zinc-900 text-zinc-900 dark:text-zinc-100 focus:outline-none focus:ring-2 focus:ring-zinc-900 dark:focus:ring-zinc-100 transition-colors disabled:opacity-50 text-sm"
        />
      </div>

      <div>
        <label for="setup-password" class="block text-sm font-medium text-zinc-700 dark:text-zinc-300 mb-1.5">
          {$t('auth.field.password')}
        </label>
        <input
          id="setup-password"
          name="password"
          type="password"
          autocomplete="new-password"
          required
          bind:value={password}
          disabled={loading}
          class="w-full px-3.5 py-2 rounded-lg border border-zinc-300 dark:border-zinc-700 bg-white dark:bg-zinc-900 text-zinc-900 dark:text-zinc-100 focus:outline-none focus:ring-2 focus:ring-zinc-900 dark:focus:ring-zinc-100 transition-colors disabled:opacity-50 text-sm"
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
            <span>{$t('auth.setup.submitting')}</span>
          {:else}
            <span>{$t('auth.setup.submit')}</span>
          {/if}
        </button>
      </div>
    </form>
  </div>
</div>
