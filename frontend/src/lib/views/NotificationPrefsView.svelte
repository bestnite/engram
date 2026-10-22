<script lang="ts">
  import SettingsSection from '../components/ui/SettingsSection.svelte';
  import { onMount } from 'svelte';
  import { t } from '../i18n';
  import { apiClient, ApiClientError } from '../api';
  import type { NotificationPrefsResponse, UpdateNotificationPrefsRequest } from '../api';
  import Select from '../components/ui/Select.svelte';
  import Checkbox from '../components/ui/Checkbox.svelte';
  import Skeleton from '../components/ui/Skeleton.svelte';
  import Button from '../components/ui/Button.svelte';
  import { toast } from '../components/ui/toast';

  interface Props {
    initialLoading?: boolean;
    initialError?: ApiClientError | Error | null;
    initialData?: NotificationPrefsResponse | null;
  }

  let { initialLoading = true, initialError = null, initialData = null }: Props = $props();

  // svelte-ignore state_referenced_locally
  let loading = $state(initialLoading);
  // svelte-ignore state_referenced_locally
  let loadError = $state<ApiClientError | Error | null>(initialError);
  // svelte-ignore state_referenced_locally
  let data = $state<NotificationPrefsResponse | null>(initialData);
  // Checkbox state is stored by type id; keys missing from the map are treated as off, consistent with the server.
  // svelte-ignore state_referenced_locally
  let choices = $state<Record<string, boolean>>(initialData ? choiceMap(initialData) : {});
  // Value of the send-time dropdown: '' = site default (null), everything else is '0'–'23'.
  // svelte-ignore state_referenced_locally
  let reminderHour = $state<string>(initialData ? hourValue(initialData.reminder_hour) : '');

  let saving = $state(false);

  const hours = Array.from({ length: 24 }, (_, h) => h);

  /** Flatten the response's per-type switches into a {type: enabled} checkbox map. */
  function choiceMap(resp: NotificationPrefsResponse): Record<string, boolean> {
    const out: Record<string, boolean> = {};
    for (const group of resp.groups) {
      for (const item of group.types) {
        out[item.type] = item.enabled;
      }
    }
    return out;
  }

  /** Send hour to dropdown value: null = site default (empty string), 0–23 become decimal strings. */
  function hourValue(hour: number | null): string {
    return hour === null || hour === undefined ? '' : String(hour);
  }

  /** Overwrite local state with the server response, so the UI only reflects values the server has confirmed. */
  function applyData(resp: NotificationPrefsResponse): void {
    data = resp;
    choices = choiceMap(resp);
    reminderHour = hourValue(resp.reminder_hour);
  }

  /** Read the preferences; being logged out is decided by the server's 401, the frontend does not guess. */
  async function load(): Promise<void> {
    loading = true;
    loadError = null;
    try {
      applyData(await apiClient.getNotificationPrefs());
    } catch (err) {
      loadError = err instanceof Error ? err : new Error(String(err));
    } finally {
      loading = false;
    }
  }

  function loadErrorKey(): string {
    if (loadError instanceof ApiClientError && loadError.isUnauthorized) return 'error.unauthorized';
    return 'settings.notifications.failed';
  }

  /** Stable error code → localized key; unknown codes fall back to the generic failure message. */
  function actionErrorKey(err: unknown): string {
    const code = err instanceof ApiClientError ? err.code : '';
    const map: Record<string, string> = {
      unknown_type: 'settings.notifications.error.unknown_type',
      class_locked: 'settings.notifications.error.class_locked',
      reminder_hour_invalid: 'settings.notifications.error.reminder_hour_invalid',
      invalid_request: 'settings.notifications.error.invalid_request',
      // The server exposes class D admin notifications to admins only: submitting that class as a non-admin is rejected with 403.
      forbidden_type: 'settings.notifications.error.forbidden_type',
    };
    return map[code] || 'settings.notifications.error.failed';
  }

  function toggle(type: string, checked: boolean): void {
    choices = { ...choices, [type]: checked };
  }

  /** Save: submit an explicit switch for each closable type, with the send time as null (site default) or 0–23. */
  async function save(event: SubmitEvent): Promise<void> {
    event.preventDefault();
    saving = true;
    const payload: UpdateNotificationPrefsRequest = {
      choices,
      reminder_hour: reminderHour === '' ? null : Number(reminderHour),
    };
    try {
      applyData(await apiClient.updateNotificationPrefs(payload));
      toast.success($t('settings.notifications.saved'));
    } catch (err) {
      toast.error($t(actionErrorKey(err)));
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

<!-- Section group for email notification preferences: embedded in the mail page (/settings/email), placed after the email address.
     It is no longer a standalone page; the small heading line above explains that the groups below are notification switches. -->
<div data-testid="notifications-view" class="mt-2 border-t border-border pt-8">
  <h2 class="text-base font-semibold text-foreground" data-testid="notifications-title">{$t('settings.notifications.heading')}</h2>
  <p class="mt-1 text-sm text-muted-foreground">{$t('settings.notifications.intro')}</p>

  {#if loading}
    <Skeleton testId="notifications-loading" label={$t('common.loading')} />
  {:else if loadError}
    <div data-testid="notifications-failed" class="py-16 text-center">
      <p role="alert" class="text-base font-medium text-foreground">{$t(loadErrorKey())}</p>
      <Button type="button" testId="notifications-retry" onclick={() => load()} variant="outline" size="lg" class="mt-4">
        {$t('common.retry')}
      </Button>
    </div>
  {:else if data}
    <form onsubmit={save} class="mt-6">
      {#each data.groups as group (group.class)}
        <SettingsSection title={$t('settings.notifications.class.' + group.class + '.heading')} testId="notifications-group-{group.class}">
          <ul class="space-y-3">
            {#each group.types as item (item.type)}
              <li class="flex items-start gap-3">
                {#if item.locked}
                  <Checkbox testId="notifications-locked-{item.type}" checked disabled class="mt-0.5" label={$t('settings.notifications.type.' + item.type)} />
                {:else}
                  <Checkbox
                    testId="notifications-type-{item.type}"
                    class="mt-0.5"
                    checked={choices[item.type] ?? false}
                    onCheckedChange={(checked) => toggle(item.type, checked)}
                    label={$t('settings.notifications.type.' + item.type)}
                  />
                {/if}
                <div>
                  <span class="text-sm text-foreground">{$t('settings.notifications.type.' + item.type)}</span>
                  {#if item.locked}
                    <p class="text-xs text-muted-foreground">{$t('settings.notifications.locked')}</p>
                  {/if}
                </div>
              </li>
            {/each}
          </ul>
        </SettingsSection>
      {/each}

      <SettingsSection
        title={$t('settings.notifications.reminder.heading')}
        description={$t('settings.notifications.reminder.hint', { tz: data.timezone })}
        testId="notifications-reminder-section"
      >
        <label class="block max-w-sm text-sm font-medium text-foreground">{$t('settings.notifications.reminder.label')}
          <Select
            class="mt-1.5"
            bind:value={reminderHour}
            testId="notifications-reminder"
            ariaLabel={$t('settings.notifications.reminder.label')}
            options={[
              { value: '', label: $t('settings.notifications.reminder.default', { hour: data.default_reminder_hour }) },
              ...hours.map((hour) => ({ value: String(hour), label: `${hour}:00` })),
            ]}
          />
        </label>
      </SettingsSection>

      <div class="flex justify-end border-t border-border pt-5">
        <Button type="submit" testId="notifications-submit" loading={saving} variant="primary" size="lg">
          {$t('settings.notifications.submit')}
        </Button>
      </div>
    </form>
  {/if}
</div>
