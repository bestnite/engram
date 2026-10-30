<script lang="ts">
  import Page from '../../components/ui/Page.svelte';
  import { onMount } from 'svelte';
  import { t, localeStore } from '../../i18n';
  import { formatBytes } from '../../format';
  import { apiClient, ApiClientError } from '../../api';
  import type { AdminSettingsResponse } from '../../api';
  import AdminNav from './AdminNav.svelte';
  import PageHeader from '../../components/ui/PageHeader.svelte';
  import SettingsSection from '../../components/ui/SettingsSection.svelte';
  import { toast } from '../../components/ui/toast';
  import Skeleton from '../../components/ui/Skeleton.svelte';
  import Button from '../../components/ui/Button.svelte';
  import { RefreshCw } from '@lucide/svelte';

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

  /** Extract the current values of the editable (non-sensitive) fields from the response as the form's initial values. */
  function seedValues(resp: AdminSettingsResponse | null): Record<string, string> {
    const out: Record<string, string> = {};
    for (const sec of resp?.sections ?? []) {
      for (const row of sec.rows) {
        if (row.editable && !row.sensitive) out[row.key] = row.value;
      }
    }
    return out;
  }

  // Settings key → frontend language pack key; an unknown key shows the key name directly (consistent with how SSR handles sensitive keys).
  const labelKeys: Record<string, string> = {
    'site.name': 'admin.settings.setting.site_name',
    'site.default_locale': 'admin.settings.setting.site_default_locale',
    media_max_bytes: 'admin.settings.setting.media_max_bytes',
    media_user_quota_bytes: 'admin.settings.setting.media_user_quota_bytes',
    optimize_min_reviews: 'admin.settings.setting.optimize_min_reviews',
    media_dir: 'admin.settings.setting.media_dir',
    media_usage: 'admin.settings.setting.media_usage',
  };
  const hintKeys: Record<string, string> = {
    'site.name': 'admin.settings.setting.site_name.hint',
    'site.default_locale': 'admin.settings.setting.site_default_locale.hint',
    media_max_bytes: 'admin.settings.setting.media_max_bytes.hint',
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

  function displayValue(row: { key: string; value: string; unit?: string }): string {
    return row.unit === 'bytes' ? formatBytes(Number(row.value) || 0, $localeStore) : row.value;
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
    // Collect the non-empty editable values and the non-empty new sensitive values; an empty string makes the server treat it as "do not modify".
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

  // Operation results are reported with a toast, not inserted as a banner at the top of the page.
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

<Page testId="admin-settings">
  <AdminNav />
  <PageHeader title={$t('admin.settings.heading')} testId="admin-settings-title" description={$t('admin.settings.intro')} />

  {#if loading}
    <Skeleton testId="admin-settings-loading" label={$t('common.loading')} lines={3} />
  {:else if loadError}
    <div data-testid="admin-settings-failed" class="py-16 text-center">
      <p role="alert" class="font-medium text-foreground">{$t(loadErrorKey())}</p>
      <Button type="button" testId="admin-settings-retry" onclick={() => load()} variant="outline" size="lg" class="mt-4"><RefreshCw class="size-4" aria-hidden="true" /><span>{$t('common.retry')}</span></Button>
    </div>
  {:else if data}
    <form onsubmit={save} data-testid="admin-settings-form">
      {#each data.sections as section (section.name)}
        <SettingsSection title={sectionLabel(section.name)} testId="admin-settings-section-{section.name}">
          <div class="max-w-2xl space-y-5">
            {#each section.rows as row (row.key)}
              <div data-testid="admin-settings-row-{row.key}">
                <p class="block text-sm font-medium text-foreground">{labelFor(row.key)}</p>
                {#if hintFor(row.key)}<p class="mt-0.5 text-xs text-muted-foreground">{hintFor(row.key)}</p>{/if}
                <div class="mt-1.5">
                  {#if !row.editable}
                    <p class="rounded-md bg-surface px-3 py-2 font-mono text-[13px] text-foreground/80">{displayValue(row)}</p>
                  {:else if row.sensitive}
                    <input
                      type="password"
                      data-testid="admin-settings-secret-{row.key}"
                      bind:value={secrets[row.key]}
                      aria-label={labelFor(row.key)}
                      placeholder={row.configured ? $t('admin.settings.sensitive.configured') : $t('admin.settings.sensitive.not_configured')}
                      class="field-input w-full text-sm"
                    />
                  {:else}
                    <input
                      data-testid="admin-settings-value-{row.key}"
                      bind:value={values[row.key]}
                      aria-label={labelFor(row.key)}
                      class="field-input w-full text-sm"
                    />
                  {/if}
                </div>
                <p class="mt-1 text-xs text-muted-foreground">{$t('admin.settings.source_label')}: {sourceLabel(row.source)}</p>
              </div>
            {/each}
          </div>
        </SettingsSection>
      {/each}
      <div class="flex justify-end border-t border-border pt-5">
        <Button type="submit" testId="admin-settings-submit" loading={saving} variant="primary" size="lg">
          {$t('admin.settings.save')}
        </Button>
      </div>
    </form>
  {/if}
</Page>
