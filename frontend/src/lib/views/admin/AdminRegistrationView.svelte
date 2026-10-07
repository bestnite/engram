<script lang="ts">
  import { onMount } from 'svelte';
  import { t } from '../../i18n';
  import { apiClient, ApiClientError } from '../../api';
  import type { AdminRegistrationResponse, AdminInvite } from '../../api';
  import AdminNav from './AdminNav.svelte';
  import Select from '../../components/ui/Select.svelte';
  import RadioGroup from '../../components/ui/RadioGroup.svelte';
  import Checkbox from '../../components/ui/Checkbox.svelte';

  interface Props {
    initialLoading?: boolean;
    initialError?: ApiClientError | Error | null;
    initialData?: AdminRegistrationResponse | null;
  }

  let { initialLoading = true, initialError = null, initialData = null }: Props = $props();

  // svelte-ignore state_referenced_locally
  let loading = $state(initialLoading);
  // svelte-ignore state_referenced_locally
  let loadError = $state<ApiClientError | Error | null>(initialError);
  // svelte-ignore state_referenced_locally
  let data = $state<AdminRegistrationResponse | null>(initialData);
  // svelte-ignore state_referenced_locally
  let policy = $state(initialData?.policy ?? 'closed');
  // svelte-ignore state_referenced_locally
  let emailDomains = $state(initialData?.email_domains ?? '');
  let notice = $state('');
  let actionError = $state('');
  let saving = $state(false);
  let form = $state<{ email: string; role: string; expires_days: string; send_email: boolean }>({
    email: '', role: 'user', expires_days: '', send_email: true,
  });

  const policies = ['open', 'invite', 'closed'];

  /** 发信结果码 → 本地化 key；空码表示未请求发信，回落到「邀请已创建」。 */
  function mailNoticeKey(code: string): string {
    const map: Record<string, string> = {
      invite_mail_queued: 'admin.registration.notice.mail_queued',
      invite_mail_unconfigured: 'admin.registration.notice.mail_unconfigured',
      invite_mail_failed: 'admin.registration.notice.mail_failed',
      invite_mail_opted_out: 'admin.registration.notice.mail_opted_out',
    };
    return map[code] || 'admin.registration.notice.invite_created';
  }

  function actionErrorKey(err: unknown): string {
    const code = err instanceof ApiClientError ? err.code : '';
    const map: Record<string, string> = {
      invalid_policy: 'admin.registration.notice.invalid_policy',
      invite_invalid: 'admin.registration.notice.invite_invalid',
      invite_create_failed: 'admin.registration.notice.invite_create_failed',
      save_failed: 'admin.registration.notice.save_failed',
    };
    return map[code] || 'admin.registration.notice.save_failed';
  }

  function statusLabel(status: string): string {
    return $t('admin.registration.status.' + status);
  }

  function inviteRoleLabel(role: string): string {
    return $t(role === 'admin' ? 'admin.users.role.admin' : 'admin.users.role.user');
  }

  async function load(): Promise<void> {
    loading = true;
    loadError = null;
    try {
      data = await apiClient.getAdminRegistration();
      policy = data.policy;
      emailDomains = data.email_domains;
    } catch (err) {
      loadError = err instanceof Error ? err : new Error(String(err));
    } finally {
      loading = false;
    }
  }

  function loadErrorKey(): string {
    if (loadError instanceof ApiClientError && (loadError.isUnauthorized || loadError.status === 403)) {
      return 'admin.error.forbidden';
    }
    return 'admin.registration.load_failed';
  }

  async function savePolicy(event: SubmitEvent): Promise<void> {
    event.preventDefault();
    notice = '';
    actionError = '';
    saving = true;
    try {
      await apiClient.saveAdminRegistration({ policy, email_domains: emailDomains });
      notice = 'admin.registration.notice.saved';
    } catch (err) {
      actionError = actionErrorKey(err);
    } finally {
      saving = false;
    }
  }

  async function createInvite(event: SubmitEvent): Promise<void> {
    event.preventDefault();
    notice = '';
    actionError = '';
    const days = form.expires_days.trim() === '' ? null : Number(form.expires_days);
    if (days !== null && (!Number.isInteger(days) || days < 0)) {
      actionError = 'admin.registration.notice.invite_create_failed';
      return;
    }
    try {
      const res = await apiClient.createAdminInvite({
        email: form.email, role: form.role, expires_days: days, send_email: form.send_email,
      });
      notice = mailNoticeKey(res.mail_notice);
      form = { email: '', role: 'user', expires_days: '', send_email: true };
      await load();
    } catch (err) {
      actionError = actionErrorKey(err);
    }
  }

  async function revoke(inv: AdminInvite): Promise<void> {
    notice = '';
    actionError = '';
    if (typeof window !== 'undefined' && !window.confirm($t('admin.registration.revoke'))) return;
    try {
      await apiClient.revokeAdminInvite(inv.id);
      notice = 'admin.registration.notice.invite_revoked';
      await load();
    } catch (err) {
      actionError = actionErrorKey(err);
    }
  }

  async function copyLink(inv: AdminInvite): Promise<void> {
    if (typeof navigator !== 'undefined' && navigator.clipboard) {
      await navigator.clipboard.writeText(inv.link);
      notice = inv.link;
    }
  }

  onMount(() => {
    if (initialData === null && initialError === null) {
      load();
    }
  });
</script>

<div class="mx-auto max-w-5xl space-y-6 px-4 py-10" data-testid="admin-registration">
  <AdminNav />

  <header class="space-y-1">
    <h1 class="text-2xl font-bold tracking-tight text-zinc-900 dark:text-zinc-100" data-testid="admin-registration-title">
      {$t('admin.registration.heading')}
    </h1>
    <p class="text-sm leading-relaxed text-zinc-600 dark:text-zinc-400">{$t('admin.registration.intro')}</p>
  </header>

  {#if notice}<div data-testid="admin-registration-notice" role="status" class="rounded-xl border border-emerald-200 bg-emerald-50 px-4 py-3 text-sm text-emerald-800 dark:border-emerald-900/60 dark:bg-emerald-950/40 dark:text-emerald-300">{$t(notice)}</div>{/if}
  {#if actionError}<div data-testid="admin-registration-error" role="alert" class="rounded-xl border border-rose-200 bg-rose-50 px-4 py-3 text-sm text-rose-700 dark:border-rose-900/60 dark:bg-rose-950/40 dark:text-rose-300">{$t(actionError)}</div>{/if}

  {#if loading}
    <div data-testid="admin-registration-loading" class="py-12 text-center text-sm text-zinc-500 dark:text-zinc-400">{$t('common.loading')}</div>
  {:else if loadError}
    <div data-testid="admin-registration-failed" class="card-elevated rounded-xl p-8 text-center">
      <p role="alert" class="font-medium text-zinc-900 dark:text-zinc-100">{$t(loadErrorKey())}</p>
      <button type="button" data-testid="admin-registration-retry" onclick={() => load()} class="btn-press mt-4 cursor-pointer rounded-lg bg-zinc-900 px-4 py-2 text-sm font-medium text-white dark:bg-zinc-100 dark:text-zinc-900">{$t('common.retry')}</button>
    </div>
  {:else if data}
    {@const view = data}
    <form onsubmit={savePolicy} data-testid="admin-registration-policy" class="card-elevated space-y-4 rounded-xl p-5">
      <h2 class="text-xs font-semibold uppercase tracking-wider text-zinc-500 dark:text-zinc-400">{$t('admin.registration.policy_heading')}</h2>
      <RadioGroup
        bind:value={policy}
        name="policy"
        itemTestIdPrefix="admin-registration-policy-"
        options={policies.map((p) => ({ value: p, label: $t('admin.registration.policy.' + p) }))}
      />
      <label class="block">
        <span class="text-xs font-semibold uppercase tracking-wider text-zinc-500 dark:text-zinc-400">{$t('admin.registration.allowlist_label')}</span>
        <input data-testid="admin-registration-domains" bind:value={emailDomains} class="mt-1.5 w-full rounded-lg border border-zinc-200 bg-white px-3 py-2 text-sm dark:border-zinc-700 dark:bg-zinc-900 dark:text-zinc-100" />
        <span class="mt-1 block text-xs text-zinc-400 dark:text-zinc-500">{$t('admin.registration.allowlist_hint')}</span>
      </label>
      <button type="submit" data-testid="admin-registration-save" disabled={saving} class="btn-press cursor-pointer rounded-lg bg-zinc-900 px-4 py-2 text-sm font-semibold text-white disabled:opacity-50 dark:bg-zinc-100 dark:text-zinc-900">{$t('admin.registration.save')}</button>
    </form>

    <section class="space-y-4">
      <h2 class="text-lg font-bold tracking-tight text-zinc-900 dark:text-zinc-100">{$t('admin.registration.invites_heading')}</h2>

      {#if view.invites.length === 0}
        <p data-testid="admin-registration-invites-empty" class="text-sm text-zinc-500 dark:text-zinc-400">{$t('admin.registration.invites_empty')}</p>
      {:else}
        <div class="card-elevated overflow-x-auto rounded-xl">
          <table class="w-full text-left text-sm">
            <thead class="border-b border-zinc-200 text-xs uppercase tracking-wider text-zinc-500 dark:border-zinc-800 dark:text-zinc-400">
              <tr>
                <th class="px-4 py-3 font-semibold">{$t('admin.registration.col.email')}</th>
                <th class="px-4 py-3 font-semibold">{$t('admin.registration.col.role')}</th>
                <th class="px-4 py-3 font-semibold">{$t('admin.registration.col.status')}</th>
                <th class="px-4 py-3 font-semibold">{$t('admin.registration.col.created')}</th>
                <th class="px-4 py-3 font-semibold">{$t('admin.registration.col.expires')}</th>
                <th class="px-4 py-3 font-semibold">{$t('admin.registration.col.used_by')}</th>
                <th class="px-4 py-3 font-semibold">{$t('admin.registration.col.actions')}</th>
              </tr>
            </thead>
            <tbody class="divide-y divide-zinc-100 dark:divide-zinc-800">
              {#each view.invites as inv (inv.id)}
                <tr data-testid="admin-registration-invite-{inv.id}">
                  <td class="px-4 py-2.5 text-zinc-700 dark:text-zinc-300">{inv.email || '—'}</td>
                  <td class="px-4 py-2.5 text-zinc-600 dark:text-zinc-400">{inviteRoleLabel(inv.role)}</td>
                  <td class="px-4 py-2.5 text-zinc-600 dark:text-zinc-400">{statusLabel(inv.status)}</td>
                  <td class="px-4 py-2.5 text-zinc-600 dark:text-zinc-400">{inv.created_at}</td>
                  <td class="px-4 py-2.5 text-zinc-600 dark:text-zinc-400">{inv.expires_at || '—'}</td>
                  <td class="px-4 py-2.5 text-zinc-600 dark:text-zinc-400">{inv.used_by || '—'}</td>
                  <td class="px-4 py-2.5">
                    <div class="flex gap-1.5">
                      <button type="button" data-testid="admin-registration-copy-{inv.id}" onclick={() => copyLink(inv)} class="cursor-pointer rounded border border-zinc-200 px-2 py-1 text-xs dark:border-zinc-700">{$t('admin.registration.copy_link')}</button>
                      {#if inv.status === 'active'}
                        <button type="button" data-testid="admin-registration-revoke-{inv.id}" onclick={() => revoke(inv)} class="cursor-pointer rounded border border-rose-200 px-2 py-1 text-xs text-rose-600 dark:border-rose-900">{$t('admin.registration.revoke')}</button>
                      {/if}
                    </div>
                  </td>
                </tr>
              {/each}
            </tbody>
          </table>
        </div>
      {/if}

      <form onsubmit={createInvite} data-testid="admin-registration-invite-create" class="card-elevated grid gap-3 rounded-xl p-5 sm:grid-cols-3">
        <h3 class="sm:col-span-3 text-xs font-semibold uppercase tracking-wider text-zinc-500 dark:text-zinc-400">{$t('admin.registration.invite_create_heading')}</h3>
        <label class="block">
          <span class="text-xs font-semibold uppercase tracking-wider text-zinc-500 dark:text-zinc-400">{$t('admin.registration.field.email')}</span>
          <input data-testid="admin-registration-invite-email" bind:value={form.email} class="mt-1.5 w-full rounded-lg border border-zinc-200 bg-white px-3 py-2 text-sm dark:border-zinc-700 dark:bg-zinc-900 dark:text-zinc-100" />
        </label>
        <label class="block">
          <span class="text-xs font-semibold uppercase tracking-wider text-zinc-500 dark:text-zinc-400">{$t('admin.registration.field.role')}</span>
          <Select
            class="mt-1.5"
            bind:value={form.role}
            testId="admin-registration-invite-role"
            options={[{ value: 'user', label: $t('admin.users.role.user') }, { value: 'admin', label: $t('admin.users.role.admin') }]}
          />
        </label>
        <label class="block">
          <span class="text-xs font-semibold uppercase tracking-wider text-zinc-500 dark:text-zinc-400">{$t('admin.registration.field.expires_days')}</span>
          <input data-testid="admin-registration-invite-expires" bind:value={form.expires_days} inputmode="numeric" class="mt-1.5 w-full rounded-lg border border-zinc-200 bg-white px-3 py-2 text-sm dark:border-zinc-700 dark:bg-zinc-900 dark:text-zinc-100" />
        </label>
        <label class="flex items-center gap-2 text-sm text-zinc-700 dark:text-zinc-300 sm:col-span-3">
          <Checkbox testId="admin-registration-invite-send" bind:checked={form.send_email} label={$t('admin.registration.field.send_email')} />
          {$t('admin.registration.field.send_email')}
        </label>
        <div class="sm:col-span-3">
          <button type="submit" data-testid="admin-registration-invite-submit" class="btn-press cursor-pointer rounded-lg bg-zinc-900 px-4 py-2 text-sm font-semibold text-white dark:bg-zinc-100 dark:text-zinc-900">{$t('admin.registration.invite_submit')}</button>
        </div>
      </form>
    </section>
  {/if}
</div>
