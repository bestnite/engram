<script lang="ts">
  import Page from '../components/ui/Page.svelte';
  import { onMount } from 'svelte';
  import { t } from '../i18n';
  import { apiClient } from '../api';
  import { getAccountErrorMessageKey } from '../api/account-errors';

  // SPA view for the email verification result (the server's GET /verify-email returns the app shell; the server no longer renders the page).
  // This view reads the token from ?token= and then consumes it via POST /api/v1/auth/verify-email (one-time, with an expiry).
    const token =
    typeof window !== 'undefined'
      ? (new URLSearchParams(window.location.search).get('token') ?? '')
      : '';

  let status = $state<'checking' | 'done' | 'error'>('checking');
  let errorKey = $state<string | null>(null);

  onMount(async () => {
    if (!token) {
      status = 'error';
      errorKey = 'account.error.token_invalid';
      return;
    }
    try {
      await apiClient.verifyEmail(token);
      status = 'done';
    } catch (err) {
      status = 'error';
      errorKey = getAccountErrorMessageKey(err);
    }
  });
</script>

<Page width="narrow">
  <div class="text-center">
    <h1 class="mb-4 text-2xl font-semibold tracking-tight text-foreground">
      {$t('account.verify.heading')}
    </h1>

    {#if status === 'checking'}
      <p data-testid="verify-checking" class="text-sm text-muted-foreground">{$t('account.verify.checking')}</p>
    {:else if status === 'done'}
      <p data-testid="verify-done" class="text-sm text-emerald-600 dark:text-emerald-400">{$t('account.verify.done')}</p>
    {:else}
      <p data-testid="verify-error" class="text-sm text-rose-600 dark:text-rose-400">{errorKey ? $t(errorKey) : $t('account.verify.failed')}</p>
    {/if}

    <div class="mt-6 text-sm">
      <a href="/login" class="text-muted-foreground hover:text-foreground transition-colors">
        {$t('account.verify.back_login')}
      </a>
    </div>
  </div>
</Page>
