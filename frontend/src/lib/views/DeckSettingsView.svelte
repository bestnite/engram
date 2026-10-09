<script lang="ts">
  import Page from '../components/ui/Page.svelte';
  import { onMount } from 'svelte';
  import { routeStore } from '../router';
  import { t } from '../i18n';
  import { apiClient, ApiClientError } from '../api';
  import type { DeckSettings } from '../api';
  import { presetDisplayName } from '../labels';
  import Select from '../components/ui/Select.svelte';
  import Skeleton from '../components/ui/Skeleton.svelte';
  import Button from '../components/ui/Button.svelte';

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

  // 卡组信息（名称/描述）只有 owner 能改；settings.role 由服务端给出。
  // svelte-ignore state_referenced_locally
  let deckName = $state(initialSettings ? initialSettings.deck_name : '');
  // svelte-ignore state_referenced_locally
  let deckDescription = $state(initialSettings ? initialSettings.deck_description : '');
  let infoSaving = $state(false);
  let infoError = $state('');
  let infoSaved = $state(false);

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
      deckName = data.deck_name;
      deckDescription = data.deck_description;
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

  /**
   * 保存卡组名称与描述（仅 owner）。空名称前端先拦下；描述可为空。
   * 400 视为名称/描述不合法，403/404 视为无权修改，其余为通用失败。
   */
  async function saveInfo(event: SubmitEvent): Promise<void> {
    event.preventDefault();
    infoError = '';
    infoSaved = false;
    const name = deckName.trim();
    if (!name) {
      infoError = 'deck.settings.info_error_invalid';
      return;
    }
    infoSaving = true;
    try {
      const updated = await apiClient.updateDeck(deckId, { name, description: deckDescription });
      deckName = updated.name;
      deckDescription = updated.description;
      if (settings) settings = { ...settings, deck_name: updated.name, deck_description: updated.description };
      infoSaved = true;
    } catch (err) {
      if (err instanceof ApiClientError && err.status === 400) {
        infoError = 'deck.settings.info_error_invalid';
      } else if (err instanceof ApiClientError && (err.isForbidden || err.isNotFound)) {
        infoError = 'deck.settings.error.forbidden';
      } else {
        infoError = 'deck.settings.info_error_failed';
      }
    } finally {
      infoSaving = false;
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

<Page {embedded} class={embedded ? 'space-y-5' : 'space-y-6'} testId="deck-settings-view">
  {#if !embedded}
    <a
      href="/decks/{encodeURIComponent(deckId)}"
      data-testid="deck-settings-back"
      class="inline-flex items-center gap-1.5 text-sm font-medium text-muted-foreground hover:text-foreground transition-colors"
    >
      <svg class="w-4 h-4" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round" aria-hidden="true"><polyline points="15 18 9 12 15 6"/></svg>
      <span>{$t('deck.settings.back')}</span>
    </a>
  {/if}

  {#if loading}
    <Skeleton testId="deck-settings-loading" label={$t('common.loading')} />
  {:else if loadError}
    <div
      data-testid={loadError instanceof ApiClientError && loadError.isForbidden ? 'deck-settings-forbidden' : 'deck-settings-failed'}
      class="card-elevated p-8 rounded-xl text-center"
    >
      <p role="alert" class="text-base font-medium text-foreground">{$t(loadErrorKey())}</p>
      <Button type="button" testId="deck-settings-retry" onclick={() => load()} variant="primary" size="lg" class="mt-4">
        {$t('common.retry')}
      </Button>
    </div>
  {:else if settings}
    {#if !embedded}
      <header>
        <h1 class="text-2xl font-semibold tracking-tight text-foreground" data-testid="deck-settings-title">
          {$t('deck.settings.title')}: {settings.deck_name}
        </h1>
      </header>
    {/if}

    {#if settings.role === 'owner'}
      <section class="card-elevated p-6 rounded-xl">
        <form onsubmit={saveInfo} data-testid="deck-info-form" class="space-y-4">
          <h2 class="text-lg font-semibold text-foreground">{$t('deck.settings.info_heading')}</h2>
          <label class="block text-sm font-medium text-foreground/80">
            {$t('deck.settings.name')}
            <input
              type="text"
              data-testid="deck-info-name"
              bind:value={deckName}
              class="field-input text-sm mt-1 block w-full"
            />
          </label>
          <label class="block text-sm font-medium text-foreground/80">
            {$t('deck.settings.description')}
            <textarea
              data-testid="deck-info-description"
              bind:value={deckDescription}
              rows="3"
              class="field-input text-sm mt-1 block w-full"
            ></textarea>
          </label>
          <div class="flex items-center gap-3">
            <Button type="submit" testId="deck-info-submit" disabled={infoSaving} variant="primary" size="lg">
              {infoSaving ? $t('deck.settings.info_saving') : $t('deck.settings.info_save')}
            </Button>
            {#if infoSaved}<p role="status" data-testid="deck-info-saved" class="text-sm text-emerald-700 dark:text-emerald-400">{$t('deck.settings.info_saved')}</p>{/if}
            {#if infoError}<p role="alert" data-testid="deck-info-error" class="text-sm text-rose-700 dark:text-rose-400">{$t(infoError)}</p>{/if}
          </div>
        </form>
      </section>
    {/if}

    <section class="card-elevated p-6 rounded-xl">
      <form onsubmit={save} data-testid="deck-settings-form" class="space-y-4">
        <h2 class="text-lg font-semibold text-foreground">{$t('deck.settings.heading')}</h2>
        <p class="text-xs text-muted-foreground" data-testid="deck-settings-personal-hint">{$t('deck.settings.personal_hint')}</p>
        <div>
          <span class="block text-sm font-medium text-foreground/80">{$t('deck.settings.preset')}</span>
          {#if presets.length > 0}
            <Select
              class="mt-1 max-w-md"
              testId="deck-settings-preset"
              value={presetId}
              onValueChange={(value) => (presetId = value)}
              options={presets}
            />
          {/if}
          <p class="mt-1 text-xs" class:text-rose-600={presetsError} class:dark:text-rose-400={presetsError} class:text-zinc-400={!presetsError} class:dark:text-zinc-500={!presetsError}>
            {presetsError ? $t('deck.settings.preset_load_failed') : $t('deck.settings.preset_hint')}
          </p>
        </div>
        <div class="grid gap-4 sm:grid-cols-2">
          <label class="block text-sm font-medium text-foreground/80">
            {$t('deck.settings.new_per_day')}
            <input
              type="number"
              min="0"
              step="1"
              data-testid="deck-settings-new-per-day"
              bind:value={newPerDay}
              class="field-input text-sm mt-1 block w-full"
            />
          </label>
          <label class="block text-sm font-medium text-foreground/80">
            {$t('deck.settings.reviews_per_day')}
            <input
              type="number"
              min="0"
              step="1"
              data-testid="deck-settings-reviews-per-day"
              bind:value={reviewsPerDay}
              class="field-input text-sm mt-1 block w-full"
            />
          </label>
        </div>
        <p class="text-xs text-muted-foreground">{$t('deck.settings.unlimited_hint')}</p>
        <div class="flex items-center gap-3">
          <Button type="submit" testId="deck-settings-submit" disabled={saving} variant="primary" size="lg">
            {saving ? $t('deck.settings.saving') : $t('deck.settings.save')}
          </Button>
          {#if saved}<p role="status" data-testid="deck-settings-saved" class="text-sm text-emerald-700 dark:text-emerald-400">{$t('deck.settings.saved')}</p>{/if}
          {#if saveError}<p role="alert" data-testid="deck-settings-save-error" class="text-sm text-rose-700 dark:text-rose-400">{$t(saveError)}</p>{/if}
        </div>
      </form>
    </section>

    <section class="card-elevated p-6 rounded-xl">
      <h2 class="text-lg font-semibold text-foreground mb-4">{$t('deck.settings.usage_heading')}</h2>
      <dl class="grid gap-3 sm:grid-cols-2 text-sm">
        <div class="flex items-center justify-between rounded-lg bg-surface px-3 py-2">
          <dt class="text-muted-foreground">{$t('deck.settings.new_used')}</dt>
          <dd class="font-medium text-foreground" data-testid="deck-settings-new-used">{settings.new_used}</dd>
        </div>
        <div class="flex items-center justify-between rounded-lg bg-surface px-3 py-2">
          <dt class="text-muted-foreground">{$t('deck.settings.new_left')}</dt>
          <dd class="font-medium text-foreground" data-testid="deck-settings-new-left">{leftText(settings.new_left, settings.new_unlimited)}</dd>
        </div>
        <div class="flex items-center justify-between rounded-lg bg-surface px-3 py-2">
          <dt class="text-muted-foreground">{$t('deck.settings.review_used')}</dt>
          <dd class="font-medium text-foreground" data-testid="deck-settings-review-used">{settings.review_used}</dd>
        </div>
        <div class="flex items-center justify-between rounded-lg bg-surface px-3 py-2">
          <dt class="text-muted-foreground">{$t('deck.settings.review_left')}</dt>
          <dd class="font-medium text-foreground" data-testid="deck-settings-review-left">{leftText(settings.review_left, settings.review_unlimited)}</dd>
        </div>
      </dl>
    </section>
  {/if}
</Page>
