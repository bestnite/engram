<script lang="ts">
  import { askConfirm } from '../../components/ui/confirm';
  import Page from '../../components/ui/Page.svelte';
  import { onMount } from 'svelte';
  import { t } from '../../i18n';
  import { apiClient, ApiClientError } from '../../api';
  import type { AdminRegistrationResponse, AdminInvite } from '../../api';
  import AdminNav from './AdminNav.svelte';
  import PageHeader from '../../components/ui/PageHeader.svelte';
  import SettingsSection from '../../components/ui/SettingsSection.svelte';
  import SegmentedControl from '../../components/ui/SegmentedControl.svelte';
  import Badge from '../../components/ui/Badge.svelte';
  import { listClasses } from '../../components/ui/variants';
  import { toast } from '../../components/ui/toast';
  import { Link2, X } from '@lucide/svelte';
  import Select from '../../components/ui/Select.svelte';
  import Checkbox from '../../components/ui/Checkbox.svelte';
  import Skeleton from '../../components/ui/Skeleton.svelte';
  import Button from '../../components/ui/Button.svelte';

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
    if (!(await askConfirm({ title: $t('admin.registration.revoke'), destructive: true }))) return;
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

<Page testId="admin-registration">
  <AdminNav />
  <PageHeader title={$t('admin.registration.heading')} testId="admin-registration-title" description={$t('admin.registration.intro')} />

  {#if loading}
    <Skeleton testId="admin-registration-loading" label={$t('common.loading')} lines={3} />
  {:else if loadError}
    <div data-testid="admin-registration-failed" class="py-16 text-center">
      <p role="alert" class="font-medium text-foreground">{$t(loadErrorKey())}</p>
      <Button type="button" testId="admin-registration-retry" onclick={() => load()} variant="outline" size="lg" class="mt-4">{$t('common.retry')}</Button>
    </div>
  {:else if data}
    {@const view = data}
    <SettingsSection title={$t('admin.registration.policy_heading')}>
      <form onsubmit={savePolicy} data-testid="admin-registration-policy" class="max-w-xl space-y-5">
        <SegmentedControl
          value={policy}
          onValueChange={(next) => (policy = next as typeof policy)}
          ariaLabel={$t('admin.registration.policy_heading')}
          itemTestIdPrefix="admin-registration-policy-"
          options={policies.map((p) => ({ value: p, label: $t('admin.registration.policy.' + p) }))}
        />
        <label class="block text-sm font-medium text-foreground">{$t('admin.registration.allowlist_label')}
          <input data-testid="admin-registration-domains" bind:value={emailDomains} class="field-input mt-1.5 w-full text-sm font-normal" />
          <span class="mt-1.5 block text-xs font-normal text-muted-foreground">{$t('admin.registration.allowlist_hint')}</span>
        </label>
        <Button type="submit" testId="admin-registration-save" disabled={saving} variant="primary" size="lg">{$t('admin.registration.save')}</Button>
      </form>
    </SettingsSection>

    <SettingsSection title={$t('admin.registration.invites_heading')} description={$t('admin.registration.invites_hint')}>
      <form onsubmit={createInvite} data-testid="admin-registration-invite-create" class="space-y-3">
        <!-- 按底部对齐：哪个标签换了行，三个输入框也仍在同一条线上。 -->
        <div class="grid items-end gap-3 sm:grid-cols-[minmax(0,2fr)_minmax(0,1fr)_minmax(0,1fr)]">
          <label class="block text-sm font-medium text-foreground">{$t('admin.registration.field.email')}
            <input data-testid="admin-registration-invite-email" type="email" bind:value={form.email} class="field-input mt-1.5 w-full text-sm font-normal" />
          </label>
          <div>
            <span class="block text-sm font-medium text-foreground">{$t('admin.registration.field.role')}</span>
            <Select
              class="mt-1.5"
              bind:value={form.role}
              testId="admin-registration-invite-role"
              ariaLabel={$t('admin.registration.field.role')}
              options={[{ value: 'user', label: $t('admin.users.role.user') }, { value: 'admin', label: $t('admin.users.role.admin') }]}
            />
          </div>
          <label class="block text-sm font-medium text-foreground">{$t('admin.registration.field.expires_days')}
            <input data-testid="admin-registration-invite-expires" bind:value={form.expires_days} inputmode="numeric" placeholder={$t('admin.registration.field.expires_placeholder')} class="field-input mt-1.5 w-full text-sm font-normal" />
          </label>
        </div>
        <div class="flex flex-wrap items-center justify-between gap-3">
          <label class="flex items-center gap-2 text-sm text-foreground">
            <Checkbox testId="admin-registration-invite-send" bind:checked={form.send_email} label={$t('admin.registration.field.send_email')} />
            {$t('admin.registration.field.send_email')}
          </label>
          <Button type="submit" testId="admin-registration-invite-submit" variant="outline" size="lg">{$t('admin.registration.invite_submit')}</Button>
        </div>
      </form>

      {#if view.invites.length === 0}
        <p data-testid="admin-registration-invites-empty" class="mt-5 text-sm text-muted-foreground">{$t('admin.registration.invites_empty')}</p>
      {:else}
        <div class="{listClasses.root} mt-5 overflow-x-auto">
          <table class="w-full min-w-[640px] text-left text-sm">
            <thead class="border-b border-border bg-surface text-xs text-muted-foreground">
              <tr>
                <th class="px-4 py-2.5 font-medium">{$t('admin.registration.col.email')}</th>
                <th class="px-4 py-2.5 font-medium">{$t('admin.registration.col.status')}</th>
                <th class="px-4 py-2.5 font-medium">{$t('admin.registration.col.expires')}</th>
                <th class="px-4 py-2.5 font-medium">{$t('admin.registration.col.used_by')}</th>
                <th class="w-24 px-4 py-2.5"><span class="sr-only">{$t('admin.registration.col.actions')}</span></th>
              </tr>
            </thead>
            <tbody class="divide-y divide-border">
              {#each view.invites as inv (inv.id)}
                <tr data-testid="admin-registration-invite-{inv.id}">
                  <td class="px-4 py-2.5">
                    <div class="text-foreground">{inv.email || '—'}</div>
                    <div class="text-[13px] text-muted-foreground">{inviteRoleLabel(inv.role)} · {inv.created_at}</div>
                  </td>
                  <td class="px-4 py-2.5"><Badge variant={inv.status === 'active' ? 'success' : 'neutral'}>{statusLabel(inv.status)}</Badge></td>
                  <td class="px-4 py-2.5 tabular-nums text-muted-foreground">{inv.expires_at || '—'}</td>
                  <td class="px-4 py-2.5 text-muted-foreground">{inv.used_by || '—'}</td>
                  <td class="px-4 py-2.5">
                    <div class="flex justify-end gap-0.5">
                      <Button variant="ghost" size="icon" testId="admin-registration-copy-{inv.id}" label={$t('admin.registration.copy_link')} title={$t('admin.registration.copy_link')} onclick={() => copyLink(inv)}>
                        <Link2 class="size-4" aria-hidden="true" />
                      </Button>
                      {#if inv.status === 'active'}
                        <Button variant="ghost" size="icon" class="hover:bg-destructive-soft hover:text-destructive-foreground" testId="admin-registration-revoke-{inv.id}" label={$t('admin.registration.revoke')} title={$t('admin.registration.revoke')} onclick={() => revoke(inv)}>
                          <X class="size-4" aria-hidden="true" />
                        </Button>
                      {/if}
                    </div>
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
