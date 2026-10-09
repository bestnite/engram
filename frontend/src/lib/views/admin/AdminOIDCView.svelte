<script lang="ts">
  import { onMount } from 'svelte';
  import { t } from '../../i18n';
  import { apiClient, ApiClientError } from '../../api';
  import type { AdminOIDCResponse, AdminOIDCIdentity, AdminTestResult } from '../../api';
  import AdminNav from './AdminNav.svelte';
  import Checkbox from '../../components/ui/Checkbox.svelte';
  import Skeleton from '../../components/ui/Skeleton.svelte';
  import Button from '../../components/ui/Button.svelte';

  interface Props {
    initialLoading?: boolean;
    initialError?: ApiClientError | Error | null;
    initialData?: AdminOIDCResponse | null;
  }

  let { initialLoading = true, initialError = null, initialData = null }: Props = $props();

  // svelte-ignore state_referenced_locally
  let loading = $state(initialLoading);
  // svelte-ignore state_referenced_locally
  let loadError = $state<ApiClientError | Error | null>(initialError);
  // svelte-ignore state_referenced_locally
  let data = $state<AdminOIDCResponse | null>(initialData);
  // svelte-ignore state_referenced_locally
  let enabled = $state(initialData?.enabled ?? false);
  // svelte-ignore state_referenced_locally
  let issuer = $state(initialData?.issuer ?? '');
  // svelte-ignore state_referenced_locally
  let clientId = $state(initialData?.client_id ?? '');
  // svelte-ignore state_referenced_locally
  let scopes = $state(initialData?.scopes ?? '');
  // svelte-ignore state_referenced_locally
  let claimSubject = $state(initialData?.claim_subject ?? '');
  // svelte-ignore state_referenced_locally
  let claimEmail = $state(initialData?.claim_email ?? '');
  // svelte-ignore state_referenced_locally
  let claimName = $state(initialData?.claim_name ?? '');
  // svelte-ignore state_referenced_locally
  let claimEmailVerified = $state(initialData?.claim_email_verified ?? '');
  let clientSecret = $state('');
  let notice = $state('');
  let actionError = $state('');
  let saving = $state(false);
  let testResult = $state<AdminTestResult | null>(null);

  function configuredLabel(configured: boolean): string {
    return $t(configured ? 'admin.oidc.configured' : 'admin.oidc.not_configured');
  }

  function applyDefaults(resp: AdminOIDCResponse): void {
    enabled = resp.enabled;
    issuer = resp.issuer;
    clientId = resp.client_id;
    scopes = resp.scopes;
    claimSubject = resp.claim_subject;
    claimEmail = resp.claim_email;
    claimName = resp.claim_name;
    claimEmailVerified = resp.claim_email_verified;
    clientSecret = '';
  }

  function loadErrorKey(): string {
    if (loadError instanceof ApiClientError && (loadError.isUnauthorized || loadError.status === 403)) {
      return 'admin.error.forbidden';
    }
    return 'admin.oidc.load_failed';
  }

  function actionErrorKey(err: unknown): string {
    const code = err instanceof ApiClientError ? err.code : '';
    const map: Record<string, string> = {
      invalid_issuer: 'admin.oidc.notice.invalid_issuer',
      save_failed: 'admin.oidc.notice.failed',
      unlink_failed: 'admin.oidc.notice.failed',
    };
    return map[code] || 'admin.oidc.notice.failed';
  }

  async function load(): Promise<void> {
    loading = true;
    loadError = null;
    try {
      data = await apiClient.getAdminOIDC();
      applyDefaults(data);
    } catch (err) {
      loadError = err instanceof Error ? err : new Error(String(err));
    } finally {
      loading = false;
    }
  }

  function payload() {
    return {
      enabled,
      issuer,
      client_id: clientId,
      scopes,
      claim_subject: claimSubject,
      claim_email: claimEmail,
      claim_name: claimName,
      claim_email_verified: claimEmailVerified,
      client_secret: clientSecret,
    };
  }

  async function save(event: SubmitEvent): Promise<void> {
    event.preventDefault();
    notice = '';
    actionError = '';
    saving = true;
    try {
      await apiClient.saveAdminOIDC(payload());
      notice = 'admin.oidc.notice.saved';
      await load();
    } catch (err) {
      actionError = actionErrorKey(err);
    } finally {
      saving = false;
    }
  }

  async function test(): Promise<void> {
    notice = '';
    actionError = '';
    testResult = null;
    try {
      testResult = await apiClient.testAdminOIDC(payload());
    } catch (err) {
      actionError = actionErrorKey(err);
    }
  }

  async function unlink(row: AdminOIDCIdentity): Promise<void> {
    notice = '';
    actionError = '';
    if (typeof window !== 'undefined' && !window.confirm($t('admin.oidc.confirm_unlink'))) return;
    try {
      await apiClient.unlinkAdminOIDCIdentity(row.id);
      notice = 'admin.oidc.notice.unlinked';
      await load();
    } catch (err) {
      actionError = actionErrorKey(err);
    }
  }

  onMount(() => {
    if (initialData === null && initialError === null) {
      load();
    }
  });
</script>

<div class="mx-auto max-w-5xl space-y-6 px-4 py-10" data-testid="admin-oidc">
  <AdminNav />

  <header class="space-y-1">
    <h1 class="text-2xl font-bold tracking-tight text-foreground" data-testid="admin-oidc-title">{$t('admin.oidc.heading')}</h1>
    <p class="text-sm leading-relaxed text-muted-foreground">{$t('admin.oidc.intro')}</p>
  </header>

  {#if notice}<div data-testid="admin-oidc-notice" role="status" class="rounded-xl border border-emerald-200 bg-emerald-50 px-4 py-3 text-sm text-emerald-800 dark:border-emerald-900/60 dark:bg-emerald-950/40 dark:text-emerald-300">{$t(notice)}</div>{/if}
  {#if actionError}<div data-testid="admin-oidc-error" role="alert" class="rounded-xl border border-rose-200 bg-rose-50 px-4 py-3 text-sm text-rose-700 dark:border-rose-900/60 dark:bg-rose-950/40 dark:text-rose-300">{$t(actionError)}</div>{/if}

  {#if loading}
    <Skeleton testId="admin-oidc-loading" label={$t('common.loading')} lines={3} />
  {:else if loadError}
    <div data-testid="admin-oidc-failed" class="card-elevated rounded-xl p-8 text-center">
      <p role="alert" class="font-medium text-foreground">{$t(loadErrorKey())}</p>
      <Button type="button" testId="admin-oidc-retry" onclick={() => load()} variant="primary" size="lg" class="mt-4">{$t('common.retry')}</Button>
    </div>
  {:else if data}
    {@const view = data}
    <form onsubmit={save} data-testid="admin-oidc-form" class="card-elevated grid gap-3 rounded-xl p-5 sm:grid-cols-2">
      <label class="flex items-center gap-2 text-sm text-foreground/80 sm:col-span-2">
        <Checkbox testId="admin-oidc-enabled" bind:checked={enabled} label={$t('admin.oidc.enabled')} />
        {$t('admin.oidc.enabled')}
      </label>
      <label class="block sm:col-span-2">
        <span class="text-xs font-semibold uppercase tracking-wider text-muted-foreground">{$t('admin.oidc.issuer')}</span>
        <input data-testid="admin-oidc-issuer" bind:value={issuer} class="field-input text-sm mt-1.5 w-full" />
      </label>
      <label class="block">
        <span class="text-xs font-semibold uppercase tracking-wider text-muted-foreground">{$t('admin.oidc.client_id')}</span>
        <input data-testid="admin-oidc-client-id" bind:value={clientId} class="field-input text-sm mt-1.5 w-full" />
      </label>
      <label class="block">
        <span class="text-xs font-semibold uppercase tracking-wider text-muted-foreground">{$t('admin.oidc.scopes')}</span>
        <input data-testid="admin-oidc-scopes" bind:value={scopes} class="field-input text-sm mt-1.5 w-full" />
      </label>
      <label class="block sm:col-span-2">
        <span class="text-xs font-semibold uppercase tracking-wider text-muted-foreground">{$t('admin.oidc.client_secret')}</span>
        <input type="password" data-testid="admin-oidc-secret" bind:value={clientSecret} placeholder={configuredLabel(view.secret_configured)} class="field-input text-sm mt-1.5 w-full" />
        <span class="mt-0.5 block text-xs text-muted-foreground/70">{$t('admin.oidc.client_secret.hint')}</span>
      </label>
      <div class="block sm:col-span-2">
        <span class="text-xs font-semibold uppercase tracking-wider text-muted-foreground">{$t('admin.oidc.redirect_uri')}</span>
        <p data-testid="admin-oidc-redirect-uri" class="mt-1.5 font-mono text-sm text-foreground/80">{view.redirect_uri}</p>
        <span class="mt-0.5 block text-xs text-muted-foreground/70">{$t('admin.oidc.redirect_uri.hint')}</span>
      </div>
      <h3 class="text-xs font-semibold uppercase tracking-wider text-muted-foreground sm:col-span-2">{$t('admin.oidc.claims')}</h3>
      <label class="block">
        <span class="text-xs text-muted-foreground">{$t('admin.oidc.claim.subject')}</span>
        <input data-testid="admin-oidc-claim-subject" bind:value={claimSubject} class="field-input text-sm mt-1 w-full" />
      </label>
      <label class="block">
        <span class="text-xs text-muted-foreground">{$t('admin.oidc.claim.email')}</span>
        <input data-testid="admin-oidc-claim-email" bind:value={claimEmail} class="field-input text-sm mt-1 w-full" />
      </label>
      <label class="block">
        <span class="text-xs text-muted-foreground">{$t('admin.oidc.claim.name')}</span>
        <input data-testid="admin-oidc-claim-name" bind:value={claimName} class="field-input text-sm mt-1 w-full" />
      </label>
      <label class="block">
        <span class="text-xs text-muted-foreground">{$t('admin.oidc.claim.email_verified')}</span>
        <input data-testid="admin-oidc-claim-email-verified" bind:value={claimEmailVerified} class="field-input text-sm mt-1 w-full" />
      </label>
      <div class="flex gap-3 sm:col-span-2">
        <Button type="submit" testId="admin-oidc-save" disabled={saving} variant="primary" size="lg">{$t('admin.oidc.save')}</Button>
        <button type="button" data-testid="admin-oidc-test" onclick={test} class="btn-press cursor-pointer rounded-lg border border-zinc-200 px-4 py-2 text-sm font-medium text-zinc-600 dark:border-zinc-700 dark:text-zinc-300">{$t('admin.oidc.test')}</button>
      </div>
    </form>

    {#if testResult}
      <div data-testid="admin-oidc-test-result" role="status" class="rounded-xl border px-4 py-3 text-sm {testResult.ok ? 'border-emerald-200 bg-emerald-50 text-emerald-800 dark:border-emerald-900/60 dark:bg-emerald-950/40 dark:text-emerald-300' : 'border-rose-200 bg-rose-50 text-rose-700 dark:border-rose-900/60 dark:bg-rose-950/40 dark:text-rose-300'}">
        {#if testResult.ok}
          {$t('admin.oidc.test.ok')}
        {:else if testResult.code === 'no_issuer'}
          {$t('admin.oidc.test.no_issuer')}
        {:else}
          {$t('admin.oidc.test.failed_prefix')} {testResult.message}
        {/if}
      </div>
    {/if}

    <section class="space-y-3" data-testid="admin-oidc-identities">
      <h2 class="text-lg font-bold tracking-tight text-foreground">{$t('admin.oidc.identities.heading')}</h2>
      {#if view.identities.length === 0}
        <p data-testid="admin-oidc-identities-empty" class="text-sm text-muted-foreground">{$t('admin.oidc.identities.empty')}</p>
      {:else}
        <div class="card-elevated overflow-x-auto rounded-xl">
          <table class="w-full text-left text-sm">
            <thead class="border-b border-zinc-200 text-xs uppercase tracking-wider text-zinc-500 dark:border-zinc-800 dark:text-zinc-400">
              <tr>
                <th class="px-4 py-3 font-semibold">{$t('admin.oidc.col.provider')}</th>
                <th class="px-4 py-3 font-semibold">{$t('admin.oidc.col.subject')}</th>
                <th class="px-4 py-3 font-semibold">{$t('admin.oidc.col.email')}</th>
                <th class="px-4 py-3 font-semibold">{$t('admin.oidc.col.user')}</th>
                <th class="px-4 py-3 font-semibold">{$t('admin.oidc.col.linked')}</th>
                <th class="px-4 py-3 font-semibold">{$t('admin.oidc.col.actions')}</th>
              </tr>
            </thead>
            <tbody class="divide-y divide-zinc-100 dark:divide-zinc-800">
              {#each view.identities as row (row.id)}
                <tr data-testid="admin-oidc-identity-{row.id}">
                  <td class="px-4 py-2.5 text-foreground/80">{row.provider}</td>
                  <td class="px-4 py-2.5 font-mono text-xs text-muted-foreground">{row.subject}</td>
                  <td class="px-4 py-2.5 text-muted-foreground">{row.email || '—'}</td>
                  <td class="px-4 py-2.5 text-muted-foreground">{row.username || '—'}</td>
                  <td class="px-4 py-2.5 text-muted-foreground">{row.linked_at}</td>
                  <td class="px-4 py-2.5">
                    <button type="button" data-testid="admin-oidc-unlink-{row.id}" onclick={() => unlink(row)} class="cursor-pointer rounded border border-rose-200 px-2 py-1 text-xs text-rose-600 dark:border-rose-900">{$t('admin.oidc.unlink')}</button>
                  </td>
                </tr>
              {/each}
            </tbody>
          </table>
        </div>
      {/if}
    </section>
  {/if}
</div>
