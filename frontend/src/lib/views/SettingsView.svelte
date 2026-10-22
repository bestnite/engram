<script lang="ts">
  import SettingsNav from './SettingsNav.svelte';
  import Page from '../components/ui/Page.svelte';
  import PageHeader from '../components/ui/PageHeader.svelte';
  import SettingsSection from '../components/ui/SettingsSection.svelte';
  import { listClasses } from '../components/ui/variants';
  import { X } from '@lucide/svelte';
  import { onMount } from 'svelte';
  import { t, localeStore, setLocale, type SupportedLocale, isSupportedLocale } from '../i18n';
  import {
    apiClient,
    ApiClientError,
    getApiErrorMessageKey,
    validateProfileForm,
    DEFAULT_DAY_CUTOFF_HOUR,
    DEFAULT_LEARN_AHEAD_MINUTES,
    MAX_LEARN_AHEAD_MINUTES,
    type UserProfile,
    type ShareAllowRow,
  } from '../api';
  import { timezoneOptions } from '../timezones';
  import Select from '../components/ui/Select.svelte';
  import Combobox from '../components/ui/Combobox.svelte';
  import Skeleton from '../components/ui/Skeleton.svelte';
  import Button from '../components/ui/Button.svelte';
  import HelpTip from '../components/ui/HelpTip.svelte';
  import { toast } from '../components/ui/toast';

  interface Props {
    initialLoading?: boolean;
  }

  // Tests use it to jump straight into the loaded state (the same pattern as NotificationPrefsView / TOTPView):
  // the app does not pass it on mount, so by default it still renders the skeleton first and then fetches.
  let { initialLoading = true }: Props = $props();

  // View reactive state (Svelte 5 runes)
  // svelte-ignore state_referenced_locally
  let loading = $state(initialLoading);
  let saving = $state(false);
  let serverApiAvailable = $state<boolean | null>(null);

  // Deck share receiving policy (consent model): who may share decks with me.
  // allowList holds the real values read back from the server (with usernames); the local side does not infer the outcome of the union operation.
  let sharePolicy = $state<'anyone' | 'whitelist' | 'nobody'>('anyone');
  let allowList = $state<ShareAllowRow[]>([]);
  let allowName = $state('');
  let shareSaving = $state(false);
  let fieldErrors = $state<Partial<Record<'display_name' | 'locale' | 'timezone' | 'day_cutoff_hour' | 'learn_ahead_minutes', string>>>({});

  // Form field bindings
  let displayName = $state('');
  let selectedLocale = $state<SupportedLocale>($localeStore);
  let timezone = $state('');
  // The day boundary must be a concrete hour: the UI offers no "unset", and old accounts whose value is NULL in the database are shown with the server default.
  let dayCutoff = $state(String(DEFAULT_DAY_CUTOFF_HOUR));
  // Early learning window (in minutes): NULL in the database shows the default value, and it becomes an explicit value after saving; 0 means disabled.
  let learnAhead = $state(String(DEFAULT_LEARN_AHEAD_MINUTES));

  // The candidates must be computed from the current value: the database may hold a historical timezone name this browser does not recognize, and the field would render empty if it were not merged into the list.
  const timezoneChoices = $derived(timezoneOptions(timezone));
  const cutoffChoices = $derived(
    Array.from({ length: 24 }, (_, hour) => ({
      value: String(hour),
      label: `${String(hour).padStart(2, '0')}:00`,
    }))
  );

  /**
   * Initialize the probe and load the existing user data
   * If the server has not implemented the JSON endpoint yet (404 Not Found), switch into local state mode safely and gracefully,
   * and strictly never guess without grounds or fabricate requests to the SSR form endpoints.
   */
  async function loadProfile(): Promise<void> {
    loading = true;

    // Default timezone fallback: try the browser's local environment timezone first
    if (typeof Intl !== 'undefined' && Intl.DateTimeFormat) {
      try {
        timezone = Intl.DateTimeFormat().resolvedOptions().timeZone || 'UTC';
      } catch {
        timezone = 'UTC';
      }
    } else {
      timezone = 'UTC';
    }

    try {
      const profile: UserProfile = await apiClient.getProfile();
      serverApiAvailable = true;
      if (profile.display_name) {
        displayName = profile.display_name;
      }
      if (isSupportedLocale(profile.locale)) {
        selectedLocale = profile.locale;
        setLocale(profile.locale);
      }
      if (profile.timezone) {
        timezone = profile.timezone;
      }
      // 0 is a legal day boundary (midnight); NULL is the unset state of old accounts, shown with the server default in the UI and becoming an explicit value after saving.
      dayCutoff = String(profile.day_cutoff_hour ?? DEFAULT_DAY_CUTOFF_HOUR);
      learnAhead = String(profile.learn_ahead_minutes ?? DEFAULT_LEARN_AHEAD_MINUTES);
    } catch (err) {
      if (err instanceof ApiClientError && err.isNotFound) {
        // The server does not provide a profile JSON endpoint yet (the API Gap report says so explicitly); degrade safely and keep the frontend's safe inputs and local application
        serverApiAvailable = false;
      } else if (err instanceof ApiClientError && err.isUnauthorized) {
        toast.error($t('error.unauthorized'));
      } else {
        toast.error($t(getApiErrorMessageKey(err)));
      }
    } finally {
      loading = false;
    }
  }

  /**
   * Switch the UI language: the frontend UI updates the reactive store immediately
   */
  function handleLocaleChange(nextLocale: string): void {
    if (isSupportedLocale(nextLocale)) {
      selectedLocale = nextLocale;
      setLocale(nextLocale);
      // Clear any lingering per-field language validation error
      if (fieldErrors.locale) {
        const { locale: _, ...rest } = fieldErrors;
        fieldErrors = rest;
      }
    }
  }

  // The header switcher and this form share the same store: once it changes the language (writing straight to the database when logged in), the form field must follow,
  // otherwise pressing save on the settings page would write the old language back as-is (the UI shows the new language while the database goes back to the old one).
  $effect(() => {
    selectedLocale = $localeStore;
  });

  /**
   * Submit the profile and settings
   */
  async function handleSubmit(event: Event): Promise<void> {
    event.preventDefault();
    fieldErrors = {};

    const rawCutoff = dayCutoff;
    const validation = validateProfileForm({
      display_name: displayName,
      locale: selectedLocale,
      timezone,
      day_cutoff_hour: rawCutoff,
      learn_ahead_minutes: learnAhead,
    });

    if (!validation.valid || !validation.data) {
      fieldErrors = validation.errors;
      return;
    }

    saving = true;

    // If the server confirms the JSON API is missing, do not fabricate requests to the SSR form; give a clear notice about local application and the gap
    if (serverApiAvailable === false) {
      toast.warning($t('settings.profile.api_unavailable'));
      saving = false;
      return;
    }

    try {
      const updated = await apiClient.updateProfile(validation.data);
      serverApiAvailable = true;
      if (isSupportedLocale(updated.locale)) {
        selectedLocale = updated.locale;
        setLocale(updated.locale);
      }
      toast.success($t('settings.profile.saved'));
    } catch (err) {
      if (err instanceof ApiClientError && err.isNotFound) {
        serverApiAvailable = false;
        toast.warning($t('settings.profile.api_unavailable'));
      } else {
        toast.error($t(getApiErrorMessageKey(err)));
      }
    } finally {
      saving = false;
    }
  }


  /** Read the current receiving policy and allowlist (GET /api/v1/settings/share-policy). */
  async function loadSharePolicy(): Promise<void> {
    try {
      const res = await apiClient.getShareInvites();
      sharePolicy = res.policy;
      allowList = res.allow_list ?? [];
    } catch {
      // Keep the default (anyone) when it cannot be read: that is exactly the true meaning of "no special setting", not an error.
    }
  }

  /**
   * Save the receiving policy (PUT /api/v1/settings/share-policy).
   *
   * After a successful save the policy and allowlist returned by the server are authoritative — the allowlist has union
   * semantics, and inferring it locally would drift. An unknown username comes back from the server as user_not_found,
   * translated into the matching copy here.
   */
  async function saveSharePolicy(input: {
    policy?: 'anyone' | 'whitelist' | 'nobody';
    allow_usernames?: string[];
    revoke?: string[];
  }): Promise<void> {
    shareSaving = true;
    try {
      const res = await apiClient.saveSharePolicy(input);
      sharePolicy = res.policy;
      allowList = res.allow_list ?? [];
      allowName = '';
      toast.success($t('settings.share_policy.saved'));
    } catch (err) {
      toast.error(
        $t(
          err instanceof ApiClientError && err.code === 'user_not_found'
            ? 'settings.share_policy.user_not_found'
            : 'settings.share_policy.failed'
        )
      );
    } finally {
      shareSaving = false;
    }
  }

  onMount(() => {
    loadProfile();
    loadSharePolicy();
  });
</script>

<Page testId="settings-view">
  <SettingsNav />
  <PageHeader title={$t('settings.heading')} description={$t('settings.intro')} />

  {#if loading}
    <Skeleton testId="settings-loading" label={$t('common.loading')} />
  {:else}
    <!-- Profile: only the profile and locale fields (password, two-factor auth and the like each have their own section) -->
    <SettingsSection title={$t('settings.profile.heading')}>
      {#if serverApiAvailable === false}
        <p data-testid="settings-api-unavailable" class="mb-4 text-[13px] text-warning" role="note">{$t('settings.profile.api_unavailable')}</p>
      {/if}
      <form onsubmit={handleSubmit} class="space-y-5" novalidate>
        <div class="grid max-w-2xl gap-5 sm:grid-cols-2">
          <div>
            <label for="settings-display-name" class="block text-sm font-medium text-foreground">{$t('settings.profile.display_name_label')}</label>
            <input id="settings-display-name" data-testid="settings-display-name" type="text" bind:value={displayName} class="field-input mt-1.5 w-full text-sm" />
            {#if fieldErrors.display_name}
              <p data-testid="settings-error-display-name" class="mt-1.5 text-xs text-destructive-foreground">{$t(fieldErrors.display_name)}</p>
            {/if}
          </div>
          <div>
            <label for="settings-locale" class="block text-sm font-medium text-foreground">{$t('settings.profile.locale_label')}</label>
            <Select
              id="settings-locale"
              value={selectedLocale}
              onValueChange={handleLocaleChange}
              testId="settings-locale"
              ariaLabel={$t('settings.profile.locale_label')}
              options={[{ value: 'zh-CN', label: $t('language.zh-CN') }, { value: 'en', label: $t('language.en') }]}
              class="mt-1.5"
            />
            {#if fieldErrors.locale}
              <p data-testid="settings-error-locale" class="mt-1.5 text-xs text-destructive-foreground">{$t(fieldErrors.locale)}</p>
            {/if}
          </div>
          <div class="sm:col-span-2">
            <label for="settings-timezone" class="block text-sm font-medium text-foreground">{$t('settings.profile.timezone_label')}</label>
            <Combobox id="settings-timezone" testId="settings-timezone" bind:value={timezone} options={timezoneChoices} ariaLabel={$t('settings.profile.timezone_label')} class="mt-1.5" />
            {#if fieldErrors.timezone}
              <p data-testid="settings-error-timezone" class="mt-1.5 text-xs text-destructive-foreground">{$t(fieldErrors.timezone)}</p>
            {/if}
          </div>
          <div>
            <label for="settings-cutoff" class="block text-sm font-medium text-foreground">{$t('settings.profile.cutoff_label')}</label>
            <Select id="settings-cutoff" testId="settings-cutoff" bind:value={dayCutoff} options={cutoffChoices} allowDeselect={false} ariaLabel={$t('settings.profile.cutoff_label')} class="mt-1.5" />
            <p class="mt-1.5 text-xs text-muted-foreground">{$t('settings.profile.cutoff_hint')}</p>
            {#if fieldErrors.day_cutoff_hour}
              <p data-testid="settings-error-cutoff" class="mt-1.5 text-xs text-destructive-foreground">{$t(fieldErrors.day_cutoff_hour)}</p>
            {/if}
          </div>
          <div>
            <div class="flex items-center gap-1">
              <label for="settings-learn-ahead" class="text-sm font-medium text-foreground">{$t('settings.profile.learn_ahead_label')}</label>
              <HelpTip testId="settings-learn-ahead-help" label={$t('settings.profile.learn_ahead_label')} text={$t('settings.profile.learn_ahead_hint')} />
            </div>
            <!-- No bind:value: binding a number input coerces the value to number, while the state here is a string,
                 validated as a "0–1440 integer" by validateLearnAheadMinutes; only a hand-written value/oninput keeps the raw text. -->
            <input id="settings-learn-ahead" data-testid="settings-learn-ahead" type="number" min="0" max={MAX_LEARN_AHEAD_MINUTES} step="1" value={learnAhead} oninput={(event) => (learnAhead = event.currentTarget.value)} class="field-input mt-1.5 w-full text-sm" />
            {#if fieldErrors.learn_ahead_minutes}
              <p data-testid="settings-error-learn-ahead" class="mt-1.5 text-xs text-destructive-foreground">{$t(fieldErrors.learn_ahead_minutes)}</p>
            {/if}
          </div>
        </div>
        <Button testId="settings-submit" type="submit" loading={saving} variant="primary" size="lg">
          {$t('settings.profile.submit')}
        </Button>
      </form>
    </SettingsSection>


    <!-- Deck share receiving policy: it blocks before an invitation is sent, so it belongs to "my preferences" rather than deck settings. -->
    <SettingsSection title={$t('settings.share_policy.heading')} description={$t('settings.share_policy.intro')} testId="settings-share-policy">
      <!-- The three-tier receiving policy uses the same dropdown style as the other settings items on this page (UI language, review day boundary, timezone). -->
      <Select
        bind:value={sharePolicy}
        testId="share-policy-options"
        allowDeselect={false}
        ariaLabel={$t('settings.share_policy.heading')}
        disabled={shareSaving}
        class="max-w-sm"
        options={[
          { value: 'anyone', label: $t('settings.share_policy.anyone') },
          { value: 'whitelist', label: $t('settings.share_policy.whitelist') },
          { value: 'nobody', label: $t('settings.share_policy.nobody') },
        ]}
        onValueChange={(next) => saveSharePolicy({ policy: next as 'anyone' | 'whitelist' | 'nobody' })}
      />

      {#if sharePolicy === 'whitelist'}
        <div class="mt-5 max-w-2xl" data-testid="share-policy-whitelist">
          <span class="block text-sm font-medium text-foreground">{$t('settings.share_policy.allow_label')}</span>
          <form
            class="mt-1.5 flex items-center gap-2"
            onsubmit={(event) => {
              event.preventDefault();
              const name = allowName.trim();
              if (name) saveSharePolicy({ allow_usernames: [name] });
            }}
          >
            <input
              type="text"
              bind:value={allowName}
              placeholder={$t('settings.share_policy.allow_placeholder')}
              aria-label={$t('settings.share_policy.allow_placeholder')}
              class="field-input min-w-0 flex-1 text-sm"
              data-testid="share-policy-allow-input"
            />
            <Button type="submit" variant="outline" size="lg" loading={shareSaving} testId="share-policy-allow-add">
              {$t('settings.share_policy.allow_add')}
            </Button>
          </form>
          {#if allowList.length === 0}
            <p class="mt-3 text-xs text-muted-foreground">{$t('settings.share_policy.allow_empty')}</p>
          {:else}
            <ul class="{listClasses.root} mt-3">
              {#each allowList as row (row.user_id)}
                <li class="flex items-center gap-3 border-b border-border px-4 py-2 last:border-b-0">
                  <span class="min-w-0 flex-1 truncate text-sm text-foreground">{row.username || '#' + row.user_id}</span>
                  <Button
                    testId="share-policy-remove-{row.user_id}"
                    variant="ghost"
                    size="icon"
                    label={$t('settings.share_policy.allow_remove')}
                    title={$t('settings.share_policy.allow_remove')}
                    disabled={shareSaving}
                    onclick={() => saveSharePolicy({ revoke: [row.user_id] })}
                  >
                    <X class="size-4" aria-hidden="true" />
                  </Button>
                </li>
              {/each}
            </ul>
          {/if}
        </div>
      {/if}
    </SettingsSection>

  {/if}
</Page>
