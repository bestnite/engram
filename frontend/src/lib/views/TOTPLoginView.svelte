<script lang="ts">
  import { onMount } from 'svelte';
  import { t } from '../i18n';
  import { navigate } from '../router';
  import { completeTOTP } from '../auth';
  import { apiClient, getApiErrorMessageKey, ApiClientError } from '../api';
  import type { ApiClient } from '../api';

  interface Props {
    client?: ApiClient;
    /** 测试注入：是否持有有效的第二步凭据；未提供时挂载后向服务端查询。 */
    initialPending?: boolean | null;
  }

  let { client = apiClient, initialPending = null }: Props = $props();

  // svelte-ignore state_referenced_locally
  let checking = $state(initialPending === null);
  // svelte-ignore state_referenced_locally
  let pending = $state<boolean>(initialPending === true);
  let code = $state('');
  let loading = $state(false);
  let errorKey = $state<string | null>(null);

  /**
   * 查询第二步是否可提交。凭据只在密码通过后下发，因此 pending=false 表示
   * 「没有待完成的第二步」（凭据缺失或已过期），此时只能回到第一步重新登录。
   */
  async function loadPending(): Promise<void> {
    checking = true;
    try {
      const res = await client.getTOTPPending();
      pending = res.pending;
    } catch {
      pending = false;
    } finally {
      checking = false;
    }
  }

  async function handleSubmit(e: SubmitEvent): Promise<void> {
    e.preventDefault();
    if (!code.trim()) {
      return;
    }
    loading = true;
    errorKey = null;
    try {
      const res = await completeTOTP(code.trim());
      if (res.authenticated) {
        navigate('/');
      }
    } catch (err) {
      if (err instanceof ApiClientError) {
        errorKey = getApiErrorMessageKey(err);
        // 凭据过期/已被消费：表单不再可提交，引导回到第一步。
        if (err.code === 'totp_challenge_expired') {
          pending = false;
        }
      } else {
        errorKey = 'error.unknown';
      }
    } finally {
      loading = false;
    }
  }

  onMount(() => {
    if (initialPending === null) {
      loadPending();
    }
  });
</script>

<div class="py-12 max-w-md mx-auto px-4">
  <div class="card-elevated p-8 rounded-xl">
    <div class="mb-6 text-center">
      <h1 class="text-2xl font-bold tracking-tight text-zinc-900 dark:text-zinc-100">
        {$t('auth.totp.heading')}
      </h1>
    </div>

    {#if checking}
      <div data-testid="totp-login-checking" class="py-8 text-center text-sm text-zinc-500 dark:text-zinc-400">
        {$t('auth.totp.checking')}
      </div>
    {:else if !pending}
      <div
        data-testid="totp-login-expired"
        class="p-4 rounded-lg bg-amber-50 dark:bg-amber-950/40 border border-amber-200 dark:border-amber-800/60 text-amber-800 dark:text-amber-200 text-sm"
      >
        <p class="font-medium mb-1">{$t('auth.totp.expired')}</p>
        <div class="mt-3">
          <a
            href="/spa/login"
            class="inline-block text-xs font-semibold px-3 py-1.5 rounded-md bg-amber-600 text-white hover:bg-amber-700 transition-colors cursor-pointer"
          >
            {$t('auth.totp.back_to_login')}
          </a>
        </div>
      </div>
    {:else}
      <p class="mb-6 text-sm text-zinc-600 dark:text-zinc-400">{$t('auth.totp.intro')}</p>

      {#if errorKey}
        <div
          data-testid="totp-login-error"
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
          <label for="totp-login-code" class="block text-sm font-medium text-zinc-700 dark:text-zinc-300 mb-1.5">
            {$t('auth.totp.code_label')}
          </label>
          <input
            id="totp-login-code"
            name="code"
            type="text"
            inputmode="numeric"
            autocomplete="one-time-code"
            required
            bind:value={code}
            disabled={loading}
            data-testid="totp-login-code"
            class="w-full px-3.5 py-2 rounded-lg border border-zinc-300 dark:border-zinc-700 bg-white dark:bg-zinc-900 text-zinc-900 dark:text-zinc-100 placeholder-zinc-400 focus:outline-none focus:ring-2 focus:ring-zinc-900 dark:focus:ring-zinc-100 transition-colors disabled:opacity-50 text-sm"
          />
          <p class="mt-2 text-xs text-zinc-500 dark:text-zinc-400">{$t('auth.totp.recovery_hint')}</p>
        </div>

        <div class="pt-2">
          <button
            type="submit"
            data-testid="totp-login-submit"
            disabled={loading || !code.trim()}
            class="w-full py-2.5 px-4 rounded-lg bg-zinc-900 dark:bg-zinc-100 text-white dark:text-zinc-900 font-semibold text-sm hover:bg-zinc-800 dark:hover:bg-zinc-200 transition-colors disabled:opacity-50 btn-press cursor-pointer flex items-center justify-center space-x-2"
          >
            {#if loading}
              <div class="w-4 h-4 border-2 border-current border-t-transparent rounded-full animate-spin" aria-hidden="true"></div>
              <span>{$t('auth.totp.submitting')}</span>
            {:else}
              <span>{$t('auth.totp.submit')}</span>
            {/if}
          </button>
        </div>
      </form>
    {/if}
  </div>
</div>
