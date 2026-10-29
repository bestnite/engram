<script lang="ts">
  import { onMount } from 'svelte';
  import { routeStore } from '../router';
  import { t } from '../i18n';
  import { apiClient, ApiClientError } from '../api';
  import type { DeckSettings } from '../api';
  import { presetDisplayName } from '../labels';
  import Select from '../components/ui/Select.svelte';
  import Skeleton from '../components/ui/Skeleton.svelte';
  import Button from '../components/ui/Button.svelte';
  import { toast } from '../components/ui/toast';
  import Page from '../components/ui/Page.svelte';
  import SettingsSection from '../components/ui/SettingsSection.svelte';
  import { ChevronLeft, RefreshCw } from '@lucide/svelte';

  interface Props {
    initialLoading?: boolean;
    initialError?: ApiClientError | Error | null;
    initialSettings?: DeckSettings | null;
    /** True when embedded in the deck details' "settings" tab: does not render the page-level shell and the back link. */
    embedded?: boolean;
  }

  let {
    initialLoading = true,
    initialError = null,
    initialSettings = null,
    embedded = false,
  }: Props = $props();

  // svelte-ignore state_referenced_locally
  let loading = $state(initialLoading);
  // svelte-ignore state_referenced_locally
  let loadError = $state<ApiClientError | Error | null>(initialError);
  // svelte-ignore state_referenced_locally
  let settings = $state<DeckSettings | null>(initialSettings);
  // svelte-ignore state_referenced_locally
  let newPerDay = $state(initialSettings ? String(initialSettings.new_per_day) : '');
  // svelte-ignore state_referenced_locally
  let reviewsPerDay = $state(initialSettings ? String(initialSettings.reviews_per_day) : '');
  // Preset list: it decides which FSRS parameter set the deck schedules with, so it sits in the same form alongside the daily cap.
  let presets = $state<Array<{ value: string; label: string }>>([]);
  // svelte-ignore state_referenced_locally
  let presetId = $state(initialSettings ? String(initialSettings.preset_id) : '');
  let presetsError = $state(false);
  let saving = $state(false);
  // Only holds local validation errors from before submission; request outcomes go through toast.
  let invalid = $state(false);

  const deckId = $derived($routeStore.params.id || '');

  /** Read the caller's own settings and today's quota; denial of access is decided by the server's 403/404, the frontend does not guess permissions. */
  /** The preset list is read once; on failure the current value is kept (saving will not carry preset_id either) and it does not silently switch to another preset. */
  async function loadPresets(): Promise<void> {
    try {
      const response = await apiClient.listPresets();
      presets = response.presets.map((item) => ({
        value: String(item.id),
        // The default preset's in-database name is a machine identifier; the display name comes from the language pack (labels.presetDisplayName).
        label: presetDisplayName(item.name, item.is_default, $t),
      }));
      presetsError = false;
    } catch {
      presetsError = true;
    }
  }

  async function load(): Promise<void> {
    if (!deckId) return;
    void loadPresets();
    loading = true;
    loadError = null;
    try {
      const data = await apiClient.getDeckSettings(deckId);
      settings = data;
      newPerDay = String(data.new_per_day);
      reviewsPerDay = String(data.reviews_per_day);
      presetId = String(data.preset_id);
    } catch (err) {
      loadError = err instanceof Error ? err : new Error(String(err));
    } finally {
      loading = false;
    }
  }

  /** Accepts non-negative integers only; 0 is legal (no limit). Empty string, negative numbers and non-numbers are all stopped on the frontend. */
  function parseCap(raw: string): number | null {
    const trimmed = raw.trim();
    if (!/^\d+$/.test(trimmed)) return null;
    const n = Number(trimmed);
    return Number.isSafeInteger(n) ? n : null;
  }

  async function save(event: SubmitEvent): Promise<void> {
    event.preventDefault();
    const n = parseCap(newPerDay);
    const r = parseCap(reviewsPerDay);
    invalid = n === null || r === null;
    if (n === null || r === null) return;
    saving = true;
    try {
      // preset_id is only submitted when a real preset is selected: omitting it leaves the preset untouched, and an empty string is not a valid preset identifier.
      const data = await apiClient.updateDeckSettings(deckId, {
        new_per_day: n,
        reviews_per_day: r,
        ...(presetId ? { preset_id: presetId } : {}),
      });
      settings = data;
      newPerDay = String(data.new_per_day);
      reviewsPerDay = String(data.reviews_per_day);
      toast.success($t('deck.settings.saved'));
    } catch (err) {
      toast.error($t(err instanceof ApiClientError && err.isForbidden ? 'deck.settings.error.forbidden' : 'deck.settings.error.failed'));
    } finally {
      saving = false;
    }
  }

  /** Display text for the remaining quota: shows "unlimited" when unlimited, never passes 0 off as unlimited. */
  function leftText(left: number, unlimited: boolean): string {
    return unlimited ? $t('deck.settings.unlimited') : String(left);
  }

  function loadErrorKey(): string {
    if (loadError instanceof ApiClientError && loadError.isUnauthorized) return 'error.unauthorized';
    if (loadError instanceof ApiClientError && loadError.isForbidden) return 'deck.settings.error.forbidden';
    if (loadError instanceof ApiClientError && loadError.isNotFound) return 'error.not_found';
    return 'deck.settings.failed';
  }

  onMount(() => {
    if (initialSettings === null && initialError === null) {
      load();
    }
  });
</script>

<Page {embedded} testId="deck-settings-view">
  {#if !embedded}
    <a
      href="/decks/{encodeURIComponent(deckId)}"
      data-testid="deck-settings-back"
      class="mb-4 inline-flex items-center gap-1.5 text-sm text-muted-foreground transition-colors hover:text-foreground"
    >
      <ChevronLeft class="size-4" aria-hidden="true" />
      <span>{$t('deck.settings.back')}</span>
    </a>
  {/if}

  {#if loading}
    <Skeleton testId="deck-settings-loading" label={$t('common.loading')} />
  {:else if loadError}
    <div
      data-testid={loadError instanceof ApiClientError && loadError.isForbidden ? 'deck-settings-forbidden' : 'deck-settings-failed'}
      class="py-16 text-center"
    >
      <p role="alert" class="text-base font-medium text-foreground">{$t(loadErrorKey())}</p>
      <Button type="button" testId="deck-settings-retry" onclick={() => load()} variant="outline" size="lg" class="mt-4">
        <RefreshCw class="size-4" aria-hidden="true" />
        <span>{$t('common.retry')}</span>
      </Button>
    </div>
  {:else if settings}
    {#if !embedded}
      <h1 class="mb-6 text-2xl font-semibold tracking-tight text-foreground" data-testid="deck-settings-title">
        {$t('deck.settings.title')}: {settings.deck_name}
      </h1>
    {/if}

    <!-- The two sections share one form and one save button: preset and daily cap are the same PATCH. -->
    <form onsubmit={save} data-testid="deck-settings-form">
      <SettingsSection title={$t('deck.settings.preset')} description={$t('deck.settings.preset_hint')}>
        {#if presets.length > 0}
          <Select
            class="max-w-sm"
            testId="deck-settings-preset"
            value={presetId}
            onValueChange={(value) => (presetId = value)}
            options={presets}
            ariaLabel={$t('deck.settings.preset')}
          />
        {/if}
        {#if presetsError}
          <p class="mt-1.5 text-xs text-destructive-foreground">{$t('deck.settings.preset_load_failed')}</p>
        {/if}
      </SettingsSection>

      <SettingsSection title={$t('deck.settings.heading')} description="{$t('deck.settings.personal_hint')} {$t('deck.settings.unlimited_hint')}">
        <p class="sr-only" data-testid="deck-settings-personal-hint">{$t('deck.settings.personal_hint')}</p>
        <!-- Number inputs never use bind:value: it coerces the value to number (an empty string becomes null), while these two
             states are strings, validated as "non-negative integer" by parseCap; only a hand-written value/oninput keeps the raw text. -->
        <div class="grid max-w-xl gap-5 sm:grid-cols-2">
          <div>
            <label class="block text-sm font-medium text-foreground" for="deck-settings-new">{$t('deck.settings.new_per_day')}</label>
            <input
              id="deck-settings-new"
              type="number"
              min="0"
              step="1"
              data-testid="deck-settings-new-per-day"
              value={newPerDay}
              oninput={(event) => (newPerDay = event.currentTarget.value)}
              class="field-input mt-1.5 block w-full text-sm"
            />
            <!-- Today's usage is written below the corresponding input: while editing the cap you can see how much is already used today. -->
            <dl class="mt-1.5 flex gap-3 text-xs text-muted-foreground">
              <div class="flex gap-1"><dt>{$t('deck.settings.used_today')}</dt><dd class="tabular-nums text-foreground" data-testid="deck-settings-new-used">{settings.new_used}</dd></div>
              <div class="flex gap-1"><dt>{$t('deck.settings.left_today')}</dt><dd class="tabular-nums text-foreground" data-testid="deck-settings-new-left">{leftText(settings.new_left, settings.new_unlimited)}</dd></div>
              {#if settings.new_extra > 0 && !settings.new_unlimited}
                <!-- Shown only after a "one more round" was appended today: explains why the remaining count is larger than the daily cap. -->
                <div class="flex gap-1"><dt>{$t('deck.settings.extra_today')}</dt><dd class="tabular-nums text-foreground" data-testid="deck-settings-new-extra">{settings.new_extra}</dd></div>
              {/if}
            </dl>
          </div>
          <div>
            <label class="block text-sm font-medium text-foreground" for="deck-settings-reviews">{$t('deck.settings.reviews_per_day')}</label>
            <input
              id="deck-settings-reviews"
              type="number"
              min="0"
              step="1"
              data-testid="deck-settings-reviews-per-day"
              value={reviewsPerDay}
              oninput={(event) => (reviewsPerDay = event.currentTarget.value)}
              class="field-input mt-1.5 block w-full text-sm"
            />
            <dl class="mt-1.5 flex gap-3 text-xs text-muted-foreground">
              <div class="flex gap-1"><dt>{$t('deck.settings.used_today')}</dt><dd class="tabular-nums text-foreground" data-testid="deck-settings-review-used">{settings.review_used}</dd></div>
              <div class="flex gap-1"><dt>{$t('deck.settings.left_today')}</dt><dd class="tabular-nums text-foreground" data-testid="deck-settings-review-left">{leftText(settings.review_left, settings.review_unlimited)}</dd></div>
            </dl>
          </div>
        </div>
      </SettingsSection>

      <div class="flex flex-wrap items-center justify-end gap-3 border-t border-border pt-5">
        {#if invalid}<p role="alert" data-testid="deck-settings-invalid" class="text-sm text-destructive-foreground">{$t('deck.settings.error.invalid')}</p>{/if}
        <Button type="submit" testId="deck-settings-submit" loading={saving} variant="primary" size="lg">
          {$t('deck.settings.save')}
        </Button>
      </div>
    </form>
  {/if}
</Page>
