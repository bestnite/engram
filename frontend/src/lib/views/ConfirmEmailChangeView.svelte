<script lang="ts">
  import Page from '../components/ui/Page.svelte';
  import { onMount } from 'svelte';
  import { t } from '../i18n';
  import { apiClient } from '../api';
  import { getAccountErrorMessageKey } from '../api/account-errors';

  // SPA view for the email change confirmation result (the server's GET /confirm-email-change returns the app shell; the server no longer renders the page).
  // This view reads the token from ?token= and then consumes it via POST /api/v1/auth/confirm-email-change (one-time, with an expiry).
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
      await apiClient.confirmEmailChange(token);
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
      {$t('account.confirm_change.heading')}
    </h1>

    {#if status === 'checking'}
      <p data-testid="confirm-change-checking" class="text-sm text-muted-foreground">{$t('account.confirm_change.checking')}</p>
    {:else if status === 'done'}
      <p data-testid="confirm-change-done" class="text-sm text-emerald-600 dark:text-emerald-400">{$t('account.confirm_change.done')}</p>
    {:else}
      <p data-testid="confirm-change-error" class="text-sm text-rose-600 dark:text-rose-400">{errorKey ? $t(errorKey) : $t('account.confirm_change.failed')}</p>
    {/if}

    <div class="mt-6 text-sm">
      <a href="/login" class="text-muted-foreground hover:text-foreground transition-colors">
        {$t('account.confirm_change.back_login')}
      </a>
    </div>
  </div>
</Page>
