<script lang="ts">
  import Page from '../components/ui/Page.svelte';
  import { onMount } from 'svelte';
  import { t } from '../i18n';
  import { navigate } from '../router';
  import { completeTOTP } from '../auth';
  import { apiClient, getApiErrorMessageKey, ApiClientError } from '../api';
  import type { ApiClient } from '../api';
  import Button from '../components/ui/Button.svelte';
  import { toast } from '../components/ui/toast';

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
    try {
      const res = await completeTOTP(code.trim());
      if (res.authenticated) {
        navigate('/');
      }
    } catch (err) {
      if (err instanceof ApiClientError) {
        toast.error($t(getApiErrorMessageKey(err)));
        // 凭据过期/已被消费：表单不再可提交，引导回到第一步。
        if (err.code === 'totp_challenge_expired') {
          pending = false;
        }
      } else {
        toast.error($t('error.unknown'));
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

<Page width="narrow">
  <div>
    <div class="mb-6 text-center">
      <h1 class="text-2xl font-semibold tracking-tight text-foreground">
        {$t('auth.totp.heading')}
      </h1>
    </div>

    {#if checking}
      <div data-testid="totp-login-checking" class="py-8 text-center text-sm text-muted-foreground">
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
            href="/login"
            class="inline-block text-xs font-semibold px-3 py-1.5 rounded-md bg-amber-600 text-white hover:bg-amber-700 transition-colors cursor-pointer"
          >
            {$t('auth.totp.back_to_login')}
          </a>
        </div>
      </div>
    {:else}
      <p class="mb-6 text-sm text-muted-foreground">{$t('auth.totp.intro')}</p>

      <form onsubmit={handleSubmit} class="space-y-4">
        <div>
          <label for="totp-login-code" class="block text-sm font-medium text-foreground/80 mb-1.5">
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
            class="field-input text-sm w-full transition-colors disabled:opacity-50"
          />
          <p class="mt-2 text-xs text-muted-foreground">{$t('auth.totp.recovery_hint')}</p>
        </div>

        <div class="pt-2">
          <Button type="submit" testId="totp-login-submit" {loading} disabled={!code.trim()} variant="primary" size="lg" class="w-full">
            {$t('auth.totp.submit')}
          </Button>
        </div>
      </form>
    {/if}
  </div>
</Page>
