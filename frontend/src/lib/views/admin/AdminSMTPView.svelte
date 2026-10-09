<script lang="ts">
  import Page from '../../components/ui/Page.svelte';
  import { onMount } from 'svelte';
  import { t } from '../../i18n';
  import { apiClient, ApiClientError } from '../../api';
  import type { AdminSMTPResponse, AdminTestResult } from '../../api';
  import AdminNav from './AdminNav.svelte';
  import PageHeader from '../../components/ui/PageHeader.svelte';
  import SettingsSection from '../../components/ui/SettingsSection.svelte';
  import { toast } from '../../components/ui/toast';
  import AdminMailTemplatesView from './AdminMailTemplatesView.svelte';
  import Select from '../../components/ui/Select.svelte';
  import Skeleton from '../../components/ui/Skeleton.svelte';
  import Button from '../../components/ui/Button.svelte';

  interface Props {
    initialLoading?: boolean;
    initialError?: ApiClientError | Error | null;
    initialData?: AdminSMTPResponse | null;
  }

  let { initialLoading = true, initialError = null, initialData = null }: Props = $props();

  // svelte-ignore state_referenced_locally
  let loading = $state(initialLoading);
  // svelte-ignore state_referenced_locally
  let loadError = $state<ApiClientError | Error | null>(initialError);
  // svelte-ignore state_referenced_locally
  let data = $state<AdminSMTPResponse | null>(initialData);
  // svelte-ignore state_referenced_locally
  let host = $state(initialData?.host ?? '');
  // svelte-ignore state_referenced_locally
  let port = $state(initialData?.port ?? '');
  // svelte-ignore state_referenced_locally
  let username = $state(initialData?.username ?? '');
  // svelte-ignore state_referenced_locally
  let from = $state(initialData?.from ?? '');
  // svelte-ignore state_referenced_locally
  let tlsMode = $state(initialData?.tls_mode ?? 'starttls');
  let password = $state('');
  let notice = $state('');
  let actionError = $state('');
  let saving = $state(false);
  let testResult = $state<AdminTestResult | null>(null);

  const tlsModes = ['none', 'starttls', 'implicit'];

  function sourceLabel(source: string): string {
    return $t('admin.settings.source.' + source);
  }

  function configuredLabel(configured: boolean): string {
    return $t(configured ? 'admin.smtp.configured' : 'admin.smtp.not_configured');
  }

  function applyDefaults(resp: AdminSMTPResponse): void {
    host = resp.host;
    port = resp.port;
    username = resp.username;
    from = resp.from;
    tlsMode = resp.tls_mode;
    password = '';
  }

  function loadErrorKey(): string {
    if (loadError instanceof ApiClientError && (loadError.isUnauthorized || loadError.status === 403)) {
      return 'admin.error.forbidden';
    }
    return 'admin.smtp.load_failed';
  }

  function actionErrorKey(err: unknown): string {
    const code = err instanceof ApiClientError ? err.code : '';
    const map: Record<string, string> = {
      invalid_port: 'admin.smtp.notice.invalid_port',
      invalid_tls_mode: 'admin.smtp.notice.invalid_tls_mode',
      save_failed: 'admin.smtp.notice.save_failed',
    };
    return map[code] || 'admin.smtp.notice.save_failed';
  }

  async function load(): Promise<void> {
    loading = true;
    loadError = null;
    try {
      data = await apiClient.getAdminSMTP();
      applyDefaults(data);
    } catch (err) {
      loadError = err instanceof Error ? err : new Error(String(err));
    } finally {
      loading = false;
    }
  }

  async function save(event: SubmitEvent): Promise<void> {
    event.preventDefault();
    notice = '';
    actionError = '';
    saving = true;
    try {
      await apiClient.saveAdminSMTP({ host, port, username, from, tls_mode: tlsMode, password });
      notice = 'admin.smtp.notice.saved';
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
      testResult = await apiClient.testAdminSMTP({ host, port, username, from, tls_mode: tlsMode, password });
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

<Page testId="admin-smtp">
  <AdminNav />
  <PageHeader title={$t('admin.smtp.heading')} testId="admin-smtp-title" />

  {#if loading}
    <Skeleton testId="admin-smtp-loading" label={$t('common.loading')} lines={3} />
  {:else if loadError}
    <div data-testid="admin-smtp-failed" class="py-16 text-center">
      <p role="alert" class="font-medium text-foreground">{$t(loadErrorKey())}</p>
      <Button type="button" testId="admin-smtp-retry" onclick={() => load()} variant="outline" size="lg" class="mt-4">{$t('common.retry')}</Button>
    </div>
  {:else if data}
    {@const view = data}
    <SettingsSection title={$t('admin.smtp.section.server')} description={$t('admin.smtp.intro')}>
      <form onsubmit={save} data-testid="admin-smtp-form" class="space-y-5">
        <div class="grid max-w-2xl gap-5 sm:grid-cols-[minmax(0,3fr)_minmax(0,1fr)]">
          <label class="block text-sm font-medium text-foreground">{$t('admin.smtp.host')}
            <input data-testid="admin-smtp-host" bind:value={host} class="field-input mt-1.5 w-full text-sm font-normal" />
            <span class="mt-1.5 block text-xs font-normal text-muted-foreground">{$t('admin.settings.source_label')}: {sourceLabel(view.host_source)}</span>
          </label>
          <label class="block text-sm font-medium text-foreground">{$t('admin.smtp.port')}
            <input data-testid="admin-smtp-port" bind:value={port} inputmode="numeric" class="field-input mt-1.5 w-full text-sm font-normal" />
          </label>
        </div>
        <div class="grid max-w-2xl gap-5 sm:grid-cols-2">
          <label class="block text-sm font-medium text-foreground">{$t('admin.smtp.username')}
            <input data-testid="admin-smtp-username" bind:value={username} class="field-input mt-1.5 w-full text-sm font-normal" />
          </label>
          <label class="block text-sm font-medium text-foreground">{$t('admin.smtp.password')}
            <input type="password" data-testid="admin-smtp-password" bind:value={password} placeholder={configuredLabel(view.password_configured)} class="field-input mt-1.5 w-full text-sm font-normal" />
            <span class="mt-1.5 block text-xs font-normal text-muted-foreground">{$t('admin.smtp.password.hint')}</span>
          </label>
          <label class="block text-sm font-medium text-foreground">{$t('admin.smtp.from')}
            <input data-testid="admin-smtp-from" bind:value={from} class="field-input mt-1.5 w-full text-sm font-normal" />
          </label>
          <div>
            <span class="block text-sm font-medium text-foreground">{$t('admin.smtp.tls_mode')}</span>
            <Select
              class="mt-1.5"
              bind:value={tlsMode}
              testId="admin-smtp-tls"
              ariaLabel={$t('admin.smtp.tls_mode')}
              options={tlsModes.map((mode) => ({ value: mode, label: $t('admin.smtp.tls.' + mode) }))}
            />
          </div>
        </div>
        <div class="flex flex-wrap items-center gap-3">
          <Button type="submit" testId="admin-smtp-save" disabled={saving} variant="primary" size="lg">{$t('admin.smtp.save')}</Button>
          <Button type="button" testId="admin-smtp-test" variant="outline" size="lg" onclick={test}>{$t('admin.smtp.test')}</Button>
          {#if testResult}
            <p data-testid="admin-smtp-test-result" role="status" class="text-sm {testResult.ok ? 'text-success' : 'text-destructive-foreground'}">
              {#if testResult.ok}
                {$t('admin.smtp.test.ok')}
              {:else if testResult.code === 'no_host'}
                {$t('admin.smtp.test.no_host')}
              {:else}
                {$t('admin.smtp.test.failed_prefix')} {testResult.message}
              {/if}
            </p>
          {/if}
        </div>
      </form>
    </SettingsSection>

    <SettingsSection title={$t('admin.smtp.outbox.heading')} testId="admin-smtp-outbox">
      <dl class="grid max-w-2xl grid-cols-2 border-y border-border sm:grid-cols-4 sm:divide-x sm:divide-border">
        <div class="px-4 py-3 first:pl-0"><dt class="text-xs text-muted-foreground">{$t('admin.smtp.outbox.configured')}</dt><dd class="mt-0.5 text-sm font-semibold text-foreground">{configuredLabel(view.configured)}</dd></div>
        <div class="px-4 py-3"><dt class="text-xs text-muted-foreground">{$t('admin.smtp.outbox.pending')}</dt><dd class="mt-0.5 text-sm font-semibold tabular-nums text-foreground">{view.outbox.pending}</dd></div>
        <div class="px-4 py-3 max-sm:pl-0"><dt class="text-xs text-muted-foreground">{$t('admin.smtp.outbox.failed')}</dt><dd class="mt-0.5 text-sm font-semibold tabular-nums {view.outbox.failed > 0 ? 'text-destructive-foreground' : 'text-foreground'}">{view.outbox.failed}</dd></div>
        <div class="px-4 py-3"><dt class="text-xs text-muted-foreground">{$t('admin.smtp.outbox.attempts')}</dt><dd class="mt-0.5 text-sm font-semibold tabular-nums text-foreground">{view.outbox.last_attempts}</dd></div>
      </dl>
      <p class="mt-3 text-[13px] text-muted-foreground">{$t('admin.smtp.outbox.last_error')}: <span class="break-all font-mono text-xs text-foreground/80">{view.outbox.last_error || $t('admin.smtp.outbox.no_error')}</span></p>
      <p class="mt-1 text-[13px] text-muted-foreground">{$t('admin.smtp.admin_notify.label')}: {configuredLabel(view.admin_notify_ready)}</p>
    </SettingsSection>
  {/if}

  <!-- 邮件模板：与 SMTP 同页（同一个「邮件」入口）。两件事本来就是一体的——
       配好发信通道之后，紧接着就是「发出去的信长什么样」。 -->
  <SettingsSection title={$t('admin.mail.heading')} description={$t('admin.mail.intro')} testId="admin-smtp-templates">
    <AdminMailTemplatesView embedded />
  </SettingsSection>
</Page>
