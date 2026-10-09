<script lang="ts">
  import Page from '../../components/ui/Page.svelte';
  import { onMount } from 'svelte';
  import { t } from '../../i18n';
  import { apiClient, ApiClientError } from '../../api';
  import type { AdminSMTPResponse, AdminTestResult } from '../../api';
  import AdminNav from './AdminNav.svelte';
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
</script>

<Page class="space-y-6" testId="admin-smtp">
  <AdminNav />

  <header class="space-y-1">
    <h1 class="text-2xl font-semibold tracking-tight text-foreground" data-testid="admin-smtp-title">{$t('admin.smtp.heading')}</h1>
    <p class="text-sm leading-relaxed text-muted-foreground">{$t('admin.smtp.intro')}</p>
  </header>

  {#if notice}<div data-testid="admin-smtp-notice" role="status" class="rounded-xl border border-emerald-200 bg-emerald-50 px-4 py-3 text-sm text-emerald-800 dark:border-emerald-900/60 dark:bg-emerald-950/40 dark:text-emerald-300">{$t(notice)}</div>{/if}
  {#if actionError}<div data-testid="admin-smtp-error" role="alert" class="rounded-xl border border-rose-200 bg-rose-50 px-4 py-3 text-sm text-rose-700 dark:border-rose-900/60 dark:bg-rose-950/40 dark:text-rose-300">{$t(actionError)}</div>{/if}

  {#if loading}
    <Skeleton testId="admin-smtp-loading" label={$t('common.loading')} lines={3} />
  {:else if loadError}
    <div data-testid="admin-smtp-failed" class="card-elevated rounded-xl p-8 text-center">
      <p role="alert" class="font-medium text-foreground">{$t(loadErrorKey())}</p>
      <Button type="button" testId="admin-smtp-retry" onclick={() => load()} variant="primary" size="lg" class="mt-4">{$t('common.retry')}</Button>
    </div>
  {:else if data}
    {@const view = data}
    <form onsubmit={save} data-testid="admin-smtp-form" class="card-elevated grid gap-3 rounded-xl p-5 sm:grid-cols-2">
      <label class="block">
        <span class="text-xs font-semibold uppercase tracking-wider text-muted-foreground">{$t('admin.smtp.host')}</span>
        <input data-testid="admin-smtp-host" bind:value={host} class="field-input text-sm mt-1.5 w-full" />
        <span class="mt-0.5 block text-xs text-muted-foreground/70">{$t('admin.settings.source_label')}: {sourceLabel(view.host_source)}</span>
      </label>
      <label class="block">
        <span class="text-xs font-semibold uppercase tracking-wider text-muted-foreground">{$t('admin.smtp.port')}</span>
        <input data-testid="admin-smtp-port" bind:value={port} inputmode="numeric" class="field-input text-sm mt-1.5 w-full" />
      </label>
      <label class="block">
        <span class="text-xs font-semibold uppercase tracking-wider text-muted-foreground">{$t('admin.smtp.username')}</span>
        <input data-testid="admin-smtp-username" bind:value={username} class="field-input text-sm mt-1.5 w-full" />
      </label>
      <label class="block">
        <span class="text-xs font-semibold uppercase tracking-wider text-muted-foreground">{$t('admin.smtp.from')}</span>
        <input data-testid="admin-smtp-from" bind:value={from} class="field-input text-sm mt-1.5 w-full" />
      </label>
      <label class="block">
        <span class="text-xs font-semibold uppercase tracking-wider text-muted-foreground">{$t('admin.smtp.tls_mode')}</span>
        <Select
          class="mt-1.5"
          bind:value={tlsMode}
          testId="admin-smtp-tls"
          options={tlsModes.map((mode) => ({ value: mode, label: $t('admin.smtp.tls.' + mode) }))}
        />
      </label>
      <label class="block">
        <span class="text-xs font-semibold uppercase tracking-wider text-muted-foreground">{$t('admin.smtp.password')}</span>
        <input type="password" data-testid="admin-smtp-password" bind:value={password} placeholder={configuredLabel(view.password_configured)} class="field-input text-sm mt-1.5 w-full" />
        <span class="mt-0.5 block text-xs text-muted-foreground/70">{$t('admin.smtp.password.hint')}</span>
      </label>
      <div class="flex gap-3 sm:col-span-2">
        <Button type="submit" testId="admin-smtp-save" disabled={saving} variant="primary" size="lg">{$t('admin.smtp.save')}</Button>
        <button type="button" data-testid="admin-smtp-test" onclick={test} class="btn-press cursor-pointer rounded-lg border border-zinc-200 px-4 py-2 text-sm font-medium text-zinc-600 dark:border-zinc-700 dark:text-zinc-300">{$t('admin.smtp.test')}</button>
      </div>
    </form>

    {#if testResult}
      <div data-testid="admin-smtp-test-result" role="status" class="rounded-xl border px-4 py-3 text-sm {testResult.ok ? 'border-emerald-200 bg-emerald-50 text-emerald-800 dark:border-emerald-900/60 dark:bg-emerald-950/40 dark:text-emerald-300' : 'border-rose-200 bg-rose-50 text-rose-700 dark:border-rose-900/60 dark:bg-rose-950/40 dark:text-rose-300'}">
        {#if testResult.ok}
          {$t('admin.smtp.test.ok')}
        {:else if testResult.code === 'no_host'}
          {$t('admin.smtp.test.no_host')}
        {:else}
          {$t('admin.smtp.test.failed_prefix')} {testResult.message}
        {/if}
      </div>
    {/if}

    <section class="card-elevated space-y-3 rounded-xl p-5" data-testid="admin-smtp-outbox">
      <h2 class="text-xs font-semibold uppercase tracking-wider text-muted-foreground">{$t('admin.smtp.outbox.heading')}</h2>
      <dl class="grid gap-2 sm:grid-cols-2">
        <div class="flex justify-between text-sm"><dt class="text-muted-foreground">{$t('admin.smtp.outbox.configured')}</dt><dd class="font-medium text-foreground">{configuredLabel(view.configured)}</dd></div>
        <div class="flex justify-between text-sm"><dt class="text-muted-foreground">{$t('admin.smtp.outbox.pending')}</dt><dd class="font-medium text-foreground">{view.outbox.pending}</dd></div>
        <div class="flex justify-between text-sm"><dt class="text-muted-foreground">{$t('admin.smtp.outbox.failed')}</dt><dd class="font-medium text-foreground">{view.outbox.failed}</dd></div>
        <div class="flex justify-between text-sm"><dt class="text-muted-foreground">{$t('admin.smtp.outbox.attempts')}</dt><dd class="font-medium text-foreground">{view.outbox.last_attempts}</dd></div>
      </dl>
      <p class="text-xs text-muted-foreground">{$t('admin.smtp.outbox.last_error')}: {view.outbox.last_error || $t('admin.smtp.outbox.no_error')}</p>
      <p class="text-sm text-muted-foreground">{$t('admin.smtp.admin_notify.label')}: {configuredLabel(view.admin_notify_ready)}</p>
    </section>
  {/if}

  <!-- 邮件模板：与 SMTP 同页（同一个「邮件」入口）。两件事本来就是一体的——
       配好发信通道之后，紧接着就是「发出去的信长什么样」。 -->
  <section class="space-y-3" data-testid="admin-smtp-templates">
    <div>
      <h2 class="text-lg font-semibold tracking-tight text-foreground">{$t('admin.mail.heading')}</h2>
      <p class="mt-1 text-sm text-muted-foreground">{$t('admin.mail.intro')}</p>
    </div>
    <AdminMailTemplatesView embedded />
  </section>
</Page>
