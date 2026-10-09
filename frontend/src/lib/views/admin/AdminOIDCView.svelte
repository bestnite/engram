<script lang="ts">
  import { askConfirm } from '../../components/ui/confirm';
  import Page from '../../components/ui/Page.svelte';
  import { onMount } from 'svelte';
  import { t } from '../../i18n';
  import { apiClient, ApiClientError } from '../../api';
  import type { AdminOIDCResponse, AdminOIDCIdentity, AdminTestResult } from '../../api';
  import AdminNav from './AdminNav.svelte';
  import PageHeader from '../../components/ui/PageHeader.svelte';
  import SettingsSection from '../../components/ui/SettingsSection.svelte';
  import { listClasses } from '../../components/ui/variants';
  import { toast } from '../../components/ui/toast';
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
    if (!(await askConfirm({ title: $t('admin.oidc.confirm_unlink'), destructive: true }))) return;
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

  // 操作结果用 toast 报告，不在页面顶部插横幅。
  $effect(() => {
    if (notice) {
      toast.success($t(notice));
      notice = '';
    }
  });
  $effect(() => {
    if (actionError) {
      toast.error($t(actionError));
      actionError = '';
    }
  });
</script>

<Page testId="admin-oidc">
  <AdminNav />
  <PageHeader title={$t('admin.oidc.heading')} testId="admin-oidc-title" description={$t('admin.oidc.intro')} />

  {#if loading}
    <Skeleton testId="admin-oidc-loading" label={$t('common.loading')} lines={3} />
  {:else if loadError}
    <div data-testid="admin-oidc-failed" class="py-16 text-center">
      <p role="alert" class="font-medium text-foreground">{$t(loadErrorKey())}</p>
      <Button type="button" testId="admin-oidc-retry" onclick={() => load()} variant="outline" size="lg" class="mt-4">{$t('common.retry')}</Button>
    </div>
  {:else if data}
    {@const view = data}
    <form onsubmit={save} data-testid="admin-oidc-form">
      <SettingsSection title={$t('admin.oidc.section.provider')} description={$t('admin.oidc.redirect_uri.hint')}>
        <div class="grid max-w-2xl gap-5 sm:grid-cols-2">
          <label class="flex items-center gap-2 text-sm text-foreground sm:col-span-2">
            <Checkbox testId="admin-oidc-enabled" bind:checked={enabled} label={$t('admin.oidc.enabled')} />
            {$t('admin.oidc.enabled')}
          </label>
          <label class="block text-sm font-medium text-foreground sm:col-span-2">{$t('admin.oidc.issuer')}
            <input data-testid="admin-oidc-issuer" bind:value={issuer} class="field-input mt-1.5 w-full text-sm font-normal" />
          </label>
          <label class="block text-sm font-medium text-foreground">{$t('admin.oidc.client_id')}
            <input data-testid="admin-oidc-client-id" bind:value={clientId} class="field-input mt-1.5 w-full text-sm font-normal" />
          </label>
          <label class="block text-sm font-medium text-foreground">{$t('admin.oidc.scopes')}
            <input data-testid="admin-oidc-scopes" bind:value={scopes} class="field-input mt-1.5 w-full text-sm font-normal" />
          </label>
          <label class="block text-sm font-medium text-foreground sm:col-span-2">{$t('admin.oidc.client_secret')}
            <input type="password" data-testid="admin-oidc-secret" bind:value={clientSecret} placeholder={configuredLabel(view.secret_configured)} class="field-input mt-1.5 w-full text-sm font-normal" />
            <span class="mt-1.5 block text-xs font-normal text-muted-foreground">{$t('admin.oidc.client_secret.hint')}</span>
          </label>
          <div class="sm:col-span-2">
            <span class="block text-sm font-medium text-foreground">{$t('admin.oidc.redirect_uri')}</span>
            <p data-testid="admin-oidc-redirect-uri" class="mt-1.5 select-all break-all rounded-md bg-surface px-3 py-2 font-mono text-[13px] text-foreground">{view.redirect_uri}</p>
          </div>
        </div>
      </SettingsSection>

      <SettingsSection title={$t('admin.oidc.claims')} description={$t('admin.oidc.claims_hint')}>
        <div class="grid max-w-2xl gap-5 sm:grid-cols-2">
          <label class="block text-sm font-medium text-foreground">{$t('admin.oidc.claim.subject')}
            <input data-testid="admin-oidc-claim-subject" bind:value={claimSubject} class="field-input mt-1.5 w-full font-mono text-sm font-normal" />
          </label>
          <label class="block text-sm font-medium text-foreground">{$t('admin.oidc.claim.email')}
            <input data-testid="admin-oidc-claim-email" bind:value={claimEmail} class="field-input mt-1.5 w-full font-mono text-sm font-normal" />
          </label>
          <label class="block text-sm font-medium text-foreground">{$t('admin.oidc.claim.name')}
            <input data-testid="admin-oidc-claim-name" bind:value={claimName} class="field-input mt-1.5 w-full font-mono text-sm font-normal" />
          </label>
          <label class="block text-sm font-medium text-foreground">{$t('admin.oidc.claim.email_verified')}
            <input data-testid="admin-oidc-claim-email-verified" bind:value={claimEmailVerified} class="field-input mt-1.5 w-full font-mono text-sm font-normal" />
          </label>
        </div>
      </SettingsSection>

      <div class="flex flex-wrap items-center justify-end gap-3 border-t border-border py-5">
        {#if testResult}
          <p data-testid="admin-oidc-test-result" role="status" class="mr-auto text-sm {testResult.ok ? 'text-success' : 'text-destructive-foreground'}">
            {#if testResult.ok}
              {$t('admin.oidc.test.ok')}
            {:else if testResult.code === 'no_issuer'}
              {$t('admin.oidc.test.no_issuer')}
            {:else}
              {$t('admin.oidc.test.failed_prefix')} {testResult.message}
            {/if}
          </p>
        {/if}
        <Button type="button" testId="admin-oidc-test" variant="outline" size="lg" onclick={test}>{$t('admin.oidc.test')}</Button>
        <Button type="submit" testId="admin-oidc-save" disabled={saving} variant="primary" size="lg">{$t('admin.oidc.save')}</Button>
      </div>
    </form>

    <SettingsSection title={$t('admin.oidc.identities.heading')} testId="admin-oidc-identities" class="first-of-type:border-t first-of-type:pt-8">
      {#if view.identities.length === 0}
        <p data-testid="admin-oidc-identities-empty" class="text-sm text-muted-foreground">{$t('admin.oidc.identities.empty')}</p>
      {:else}
        <div class="{listClasses.root} overflow-x-auto">
          <table class="w-full min-w-[560px] text-left text-sm">
            <thead class="border-b border-border bg-surface text-xs text-muted-foreground">
              <tr>
                <th class="px-4 py-2.5 font-medium">{$t('admin.oidc.col.user')}</th>
                <th class="px-4 py-2.5 font-medium">{$t('admin.oidc.col.subject')}</th>
                <th class="px-4 py-2.5 font-medium">{$t('admin.oidc.col.linked')}</th>
                <th class="w-20 px-4 py-2.5"><span class="sr-only">{$t('admin.oidc.col.actions')}</span></th>
              </tr>
            </thead>
            <tbody class="divide-y divide-border">
              {#each view.identities as row (row.id)}
                <tr data-testid="admin-oidc-identity-{row.id}">
                  <td class="px-4 py-2.5">
                    <div class="text-foreground">{row.username || '—'}</div>
                    <div class="text-[13px] text-muted-foreground">{row.email || '—'} · {row.provider}</div>
                  </td>
                  <td class="max-w-56 truncate px-4 py-2.5 font-mono text-xs text-muted-foreground" title={row.subject}>{row.subject}</td>
                  <td class="px-4 py-2.5 tabular-nums text-muted-foreground">{row.linked_at}</td>
                  <td class="px-4 py-2.5 text-right">
                    <Button variant="ghost" size="sm" class="hover:bg-destructive-soft hover:text-destructive-foreground" testId="admin-oidc-unlink-{row.id}" onclick={() => unlink(row)}>{$t('admin.oidc.unlink')}</Button>
                  </td>
                </tr>
              {/each}
            </tbody>
          </table>
        </div>
      {/if}
    </SettingsSection>
  {/if}
</Page>
