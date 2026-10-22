<script lang="ts">
  import Page from '../components/ui/Page.svelte';
  import { t } from '../i18n';
  import { apiClient } from '../api';
  import { getAccountErrorMessageKey } from '../api/account-errors';
  import Button from '../components/ui/Button.svelte';
  import { toast } from '../components/ui/toast';

  // Password reset request (after the server's GET /forgot-password switched to the shell, the client router renders this page).
  // The protocol is POST /api/v1/auth/forgot-password: it returns a same-shaped response whether or not the account exists, avoiding account enumeration.
  let email = $state('');
  let loading = $state(false);
  let done = $state(false);
  let mailReady = $state(true);

  async function handleSubmit(e: SubmitEvent): Promise<void> {
    e.preventDefault();
    loading = true;
    try {
      const res = await apiClient.requestPasswordReset({ email: email.trim() });
      mailReady = res.mail_ready;
      done = true;
    } catch (err) {
      toast.error($t(getAccountErrorMessageKey(err)));
    } finally {
      loading = false;
    }
  }
</script>

<Page width="narrow">
  <div>
    <div class="mb-6 text-center">
      <h1 class="text-2xl font-semibold tracking-tight text-foreground">
        {$t('account.forgot.heading')}
      </h1>
    </div>

    {#if done}
      <div
        data-testid="forgot-done"
        class="mb-6 p-4 rounded-lg bg-muted border border-input text-foreground/80 text-sm"
      >
        <span>{mailReady ? $t('account.forgot.sent') : $t('account.error.mail_not_configured')}</span>
      </div>
      <a
        href="/login"
        class="block text-center text-sm text-muted-foreground hover:text-foreground transition-colors"
      >
        {$t('account.forgot.back_login')}
      </a>
    {:else}
      <p class="mb-6 text-sm text-muted-foreground">{$t('account.forgot.intro')}</p>

      <form onsubmit={handleSubmit} class="space-y-4">
        <div>
          <label for="forgot-email" class="block text-sm font-medium text-foreground/80 mb-1.5">
            {$t('account.forgot.email_label')}
          </label>
          <input
            id="forgot-email"
            data-testid="forgot-email"
            name="email"
            type="email"
            autocomplete="email"
            required
            bind:value={email}
            disabled={loading}
            class="field-input text-sm w-full transition-colors disabled:opacity-50"
          />
        </div>

        <div class="pt-2">
          <Button type="submit" {loading} disabled={!email.trim()} variant="primary" size="lg" class="w-full" testId="forgot-submit">
            {$t('account.forgot.submit')}
          </Button>
        </div>
      </form>

      <div class="mt-6 text-center text-sm">
        <a href="/login" class="text-muted-foreground hover:text-foreground transition-colors">
          {$t('account.forgot.back_login')}
        </a>
      </div>
    {/if}
  </div>
</Page>
