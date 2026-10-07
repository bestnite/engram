<script lang="ts">
  import { onMount } from 'svelte';
  import { t } from '../../i18n';
  import { apiClient, ApiClientError } from '../../api';
  import type { AdminSettingsResponse } from '../../api';
  import AdminNav from './AdminNav.svelte';
  import Skeleton from '../../components/ui/Skeleton.svelte';
  import Button from '../../components/ui/Button.svelte';

  interface Props {
    initialLoading?: boolean;
    initialError?: ApiClientError | Error | null;
    initialData?: AdminSettingsResponse | null;
  }

  let { initialLoading = true, initialError = null, initialData = null }: Props = $props();

  // svelte-ignore state_referenced_locally
  let loading = $state(initialLoading);
  // svelte-ignore state_referenced_locally
  let loadError = $state<ApiClientError | Error | null>(initialError);
  // svelte-ignore state_referenced_locally
  let data = $state<AdminSettingsResponse | null>(initialData);
  // svelte-ignore state_referenced_locally
  let values = $state<Record<string, string>>(seedValues(initialData));
  let secrets = $state<Record<string, string>>({});
  let notice = $state('');
  let actionError = $state('');
  let saving = $state(false);

  /** 从响应里抽出可编辑（非敏感）字段的当前值作为表单初值。 */
  function seedValues(resp: AdminSettingsResponse | null): Record<string, string> {
    const out: Record<string, string> = {};
    for (const sec of resp?.sections ?? []) {
      for (const row of sec.rows) {
        if (row.editable && !row.sensitive) out[row.key] = row.value;
      }
    }
    return out;
  }

  // 设置键 → 前端语言包键；未知键直接显示键名（与 SSR 对敏感键的处理一致）。
  const labelKeys: Record<string, string> = {
    'site.name': 'admin.settings.setting.site_name',
    'site.default_locale': 'admin.settings.setting.site_default_locale',
    media_max_bytes: 'admin.settings.setting.media_max_bytes',
    media_allowed_mimes: 'admin.settings.setting.media_allowed_mimes',
    media_user_quota_bytes: 'admin.settings.setting.media_user_quota_bytes',
    optimize_min_reviews: 'admin.settings.setting.optimize_min_reviews',
    media_dir: 'admin.settings.setting.media_dir',
    media_usage: 'admin.settings.setting.media_usage',
  };
  const hintKeys: Record<string, string> = {
    'site.name': 'admin.settings.setting.site_name.hint',
    'site.default_locale': 'admin.settings.setting.site_default_locale.hint',
    media_max_bytes: 'admin.settings.setting.media_max_bytes.hint',
    media_allowed_mimes: 'admin.settings.setting.media_allowed_mimes.hint',
    media_user_quota_bytes: 'admin.settings.setting.media_user_quota_bytes.hint',
    optimize_min_reviews: 'admin.settings.setting.optimize_min_reviews.hint',
    media_dir: 'admin.settings.setting.media_dir.hint',
  };

  function labelFor(key: string): string {
    return labelKeys[key] ? $t(labelKeys[key]) : key;
  }

  function hintFor(key: string): string {
    return hintKeys[key] ? $t(hintKeys[key]) : '';
  }

  function sectionLabel(name: string): string {
    return $t('admin.settings.section.' + name);
  }

  function sourceLabel(source: string): string {
    return $t('admin.settings.source.' + source);
  }

  function formatBytes(n: number): string {
    if (n < 1024) return `${n} B`;
    const units = ['KiB', 'MiB', 'GiB', 'TiB'];
    let value = n;
    for (let i = 0; i < units.length; i++) {
      value /= 1024;
      if (value < 1024 || i === units.length - 1) return `${value.toFixed(2)} ${units[i]}`;
    }
    return `${n} B`;
  }

  function displayValue(row: { key: string; value: string; unit?: string }): string {
    return row.unit === 'bytes' ? formatBytes(Number(row.value) || 0) : row.value;
  }

  function loadErrorKey(): string {
    if (loadError instanceof ApiClientError && (loadError.isUnauthorized || loadError.status === 403)) {
      return 'admin.error.forbidden';
    }
    return 'admin.settings.load_failed';
  }

  function actionErrorKey(err: unknown): string {
    const code = err instanceof ApiClientError ? err.code : '';
    const map: Record<string, string> = {
      invalid_locale: 'admin.settings.notice.invalid_locale',
      invalid_number: 'admin.settings.notice.invalid_number',
      invalid_mime: 'admin.settings.notice.invalid_mime',
      optimize_min_reviews_too_low: 'admin.settings.notice.optimize_min_reviews_too_low',
      save_failed: 'admin.settings.notice.save_failed',
    };
    return map[code] || 'admin.settings.notice.save_failed';
  }

  async function load(): Promise<void> {
    loading = true;
    loadError = null;
    try {
      data = await apiClient.getAdminSettings();
      values = seedValues(data);
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
    // 汇总非空的可编辑值与非空的敏感新值；空串让服务端按「不修改」处理。
    const payload: Record<string, string> = {};
    for (const [key, value] of Object.entries(values)) if (value !== '') payload[key] = value;
    for (const [key, value] of Object.entries(secrets)) if (value !== '') payload[key] = value;
    try {
      await apiClient.saveAdminSettings(payload);
      notice = 'admin.settings.notice.saved';
      secrets = {};
      await load();
    } catch (err) {
      actionError = actionErrorKey(err);
    } finally {
      saving = false;
    }
  }

  onMount(() => {
    if (initialData === null && initialError === null) {
      load();
    }
  });
</script>

<div class="mx-auto max-w-5xl space-y-6 px-4 py-10" data-testid="admin-settings">
  <AdminNav />

  <header class="space-y-1">
    <h1 class="text-2xl font-bold tracking-tight text-zinc-900 dark:text-zinc-100" data-testid="admin-settings-title">{$t('admin.settings.heading')}</h1>
    <p class="text-sm leading-relaxed text-zinc-600 dark:text-zinc-400">{$t('admin.settings.intro')}</p>
  </header>

  {#if notice}<div data-testid="admin-settings-notice" role="status" class="rounded-xl border border-emerald-200 bg-emerald-50 px-4 py-3 text-sm text-emerald-800 dark:border-emerald-900/60 dark:bg-emerald-950/40 dark:text-emerald-300">{$t(notice)}</div>{/if}
  {#if actionError}<div data-testid="admin-settings-error" role="alert" class="rounded-xl border border-rose-200 bg-rose-50 px-4 py-3 text-sm text-rose-700 dark:border-rose-900/60 dark:bg-rose-950/40 dark:text-rose-300">{$t(actionError)}</div>{/if}

  {#if loading}
    <Skeleton testId="admin-settings-loading" label={$t('common.loading')} lines={3} />
  {:else if loadError}
    <div data-testid="admin-settings-failed" class="card-elevated rounded-xl p-8 text-center">
      <p role="alert" class="font-medium text-zinc-900 dark:text-zinc-100">{$t(loadErrorKey())}</p>
      <Button type="button" testId="admin-settings-retry" onclick={() => load()} variant="primary" size="lg" class="mt-4">{$t('common.retry')}</Button>
    </div>
  {:else if data}
    <form onsubmit={save} class="space-y-6" data-testid="admin-settings-form">
      {#each data.sections as section (section.name)}
        <section data-testid="admin-settings-section-{section.name}" class="card-elevated space-y-4 rounded-xl p-5">
          <h2 class="text-xs font-semibold uppercase tracking-wider text-zinc-500 dark:text-zinc-400">{sectionLabel(section.name)}</h2>
          <div class="space-y-4">
            {#each section.rows as row (row.key)}
              <div data-testid="admin-settings-row-{row.key}" class="grid gap-1.5 sm:grid-cols-[minmax(0,1fr)_minmax(0,2fr)] sm:items-center">
                <div>
                  <p class="text-sm font-medium text-zinc-700 dark:text-zinc-300">{labelFor(row.key)}</p>
                  {#if hintFor(row.key)}<p class="text-xs text-zinc-400 dark:text-zinc-500">{hintFor(row.key)}</p>{/if}
                </div>
                <div>
                  {#if !row.editable}
                    <p class="text-sm font-mono text-zinc-600 dark:text-zinc-400">{displayValue(row)}</p>
                  {:else if row.sensitive}
                    <input
                      type="password"
                      data-testid="admin-settings-secret-{row.key}"
                      bind:value={secrets[row.key]}
                      placeholder={row.configured ? $t('admin.settings.sensitive.configured') : $t('admin.settings.sensitive.not_configured')}
                      class="field-input text-sm w-full"
                    />
                  {:else}
                    <input
                      data-testid="admin-settings-value-{row.key}"
                      bind:value={values[row.key]}
                      class="field-input text-sm w-full"
                    />
                  {/if}
                  <p class="mt-0.5 text-xs text-zinc-400 dark:text-zinc-500">{$t('admin.settings.source_label')}: {sourceLabel(row.source)}</p>
                </div>
              </div>
            {/each}
          </div>
        </section>
      {/each}
      <Button type="submit" testId="admin-settings-submit" disabled={saving} variant="primary" size="lg">
        {$t('admin.settings.save')}
      </Button>
    </form>
  {/if}
</div>
