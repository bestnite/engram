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
  import Page from '../components/ui/Page.svelte';
  import SettingsSection from '../components/ui/SettingsSection.svelte';

  interface Props {
    initialLoading?: boolean;
    initialError?: ApiClientError | Error | null;
    initialSettings?: DeckSettings | null;
    /** 嵌在卡组详情「设置」Tab 内时为真：不渲染页面级外壳与返回链接。 */
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
  // 预设列表：卡组按哪套 FSRS 参数排程由它决定，所以和每日额度并列在同一个表单里。
  let presets = $state<Array<{ value: string; label: string }>>([]);
  // svelte-ignore state_referenced_locally
  let presetId = $state(initialSettings ? String(initialSettings.preset_id) : '');
  let presetsError = $state(false);
  let saving = $state(false);
  let saveError = $state('');
  let saved = $state(false);

  const deckId = $derived($routeStore.params.id || '');

  /** 读取调用者自己的设置与今日额度；无权访问由服务端 403/404 决定，前端不猜测权限。 */
  /** 预设列表只读一次；失败时保留当前值（保存也不会带上 preset_id），不静默改成别的预设。 */
  async function loadPresets(): Promise<void> {
    try {
      const response = await apiClient.listPresets();
      presets = response.presets.map((item) => ({
        value: String(item.id),
        // 默认预设的库内名是机器标识，显示名走语言包（labels.presetDisplayName）。
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

  /** 只接受非负整数；0 合法（不限）。空串、负数、非数字一律前端拦下。 */
  function parseCap(raw: string): number | null {
    const trimmed = raw.trim();
    if (!/^\d+$/.test(trimmed)) return null;
    const n = Number(trimmed);
    return Number.isSafeInteger(n) ? n : null;
  }

  async function save(event: SubmitEvent): Promise<void> {
    event.preventDefault();
    saveError = '';
    saved = false;
    const n = parseCap(newPerDay);
    const r = parseCap(reviewsPerDay);
    if (n === null || r === null) {
      saveError = 'deck.settings.error.invalid';
      return;
    }
    saving = true;
    try {
      // preset_id 只在选中了一个真实预设时提交：省略即不动预设，空串不是合法预设标识。
      const data = await apiClient.updateDeckSettings(deckId, {
        new_per_day: n,
        reviews_per_day: r,
        ...(presetId ? { preset_id: presetId } : {}),
      });
      settings = data;
      newPerDay = String(data.new_per_day);
      reviewsPerDay = String(data.reviews_per_day);
      saved = true;
    } catch (err) {
      if (err instanceof ApiClientError && err.isForbidden) {
        saveError = 'deck.settings.error.forbidden';
      } else {
        saveError = 'deck.settings.error.failed';
      }
    } finally {
      saving = false;
    }
  }

  /** 剩余额度的展示文本：不限时显示「不限」，绝不拿 0 冒充不限。 */
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
      <svg class="size-4" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round" aria-hidden="true"><polyline points="15 18 9 12 15 6"/></svg>
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
        {$t('common.retry')}
      </Button>
    </div>
  {:else if settings}
    {#if !embedded}
      <h1 class="mb-6 text-2xl font-semibold tracking-tight text-foreground" data-testid="deck-settings-title">
        {$t('deck.settings.title')}: {settings.deck_name}
      </h1>
    {/if}

    <!-- 两个分区共用一个表单与一个保存按钮：预设与每日上限是同一次 PATCH。 -->
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
        <div class="grid max-w-xl gap-5 sm:grid-cols-2">
          <div>
            <label class="block text-sm font-medium text-foreground" for="deck-settings-new">{$t('deck.settings.new_per_day')}</label>
            <input
              id="deck-settings-new"
              type="number"
              min="0"
              step="1"
              data-testid="deck-settings-new-per-day"
              bind:value={newPerDay}
              class="field-input mt-1.5 block w-full text-sm"
            />
            <!-- 今日用量写在对应输入框下面：改上限时就能看到今天已经用了多少。 -->
            <dl class="mt-1.5 flex gap-3 text-xs text-muted-foreground">
              <div class="flex gap-1"><dt>{$t('deck.settings.used_today')}</dt><dd class="tabular-nums text-foreground" data-testid="deck-settings-new-used">{settings.new_used}</dd></div>
              <div class="flex gap-1"><dt>{$t('deck.settings.left_today')}</dt><dd class="tabular-nums text-foreground" data-testid="deck-settings-new-left">{leftText(settings.new_left, settings.new_unlimited)}</dd></div>
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
              bind:value={reviewsPerDay}
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
        {#if saveError}<p role="alert" data-testid="deck-settings-save-error" class="text-sm text-destructive-foreground">{$t(saveError)}</p>{/if}
        {#if saved}<p role="status" data-testid="deck-settings-saved" class="text-sm text-success">{$t('deck.settings.saved')}</p>{/if}
        <Button type="submit" testId="deck-settings-submit" disabled={saving} variant="primary" size="lg">
          {saving ? $t('deck.settings.saving') : $t('deck.settings.save')}
        </Button>
      </div>
    </form>
  {/if}
</Page>
