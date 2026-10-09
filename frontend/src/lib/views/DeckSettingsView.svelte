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

<div class={embedded ? 'space-y-5' : 'mx-auto max-w-4xl space-y-6 px-4 py-10'} data-testid="deck-settings-view">
  {#if !embedded}
    <a
      href="/decks/{encodeURIComponent(deckId)}"
      data-testid="deck-settings-back"
      class="inline-flex items-center gap-1.5 text-sm font-medium text-zinc-500 dark:text-zinc-400 hover:text-zinc-900 dark:hover:text-zinc-100 transition-colors"
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
      <p role="alert" class="text-base font-medium text-zinc-900 dark:text-zinc-100">{$t(loadErrorKey())}</p>
      <Button type="button" testId="deck-settings-retry" onclick={() => load()} variant="primary" size="lg" class="mt-4">
        {$t('common.retry')}
      </Button>
    </div>
  {:else if settings}
    {#if !embedded}
      <header>
        <h1 class="text-2xl font-bold tracking-tight text-zinc-900 dark:text-zinc-100" data-testid="deck-settings-title">
          {$t('deck.settings.title')}: {settings.deck_name}
        </h1>
      </header>
    {/if}

    <section class="card-elevated p-6 rounded-xl">
      <form onsubmit={save} data-testid="deck-settings-form" class="space-y-4">
        <h2 class="text-lg font-semibold text-zinc-900 dark:text-zinc-100">{$t('deck.settings.heading')}</h2>
        <p class="text-xs text-zinc-500 dark:text-zinc-400" data-testid="deck-settings-personal-hint">{$t('deck.settings.personal_hint')}</p>
        <div>
          <span class="block text-sm font-medium text-zinc-700 dark:text-zinc-300">{$t('deck.settings.preset')}</span>
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
          <label class="block text-sm font-medium text-zinc-700 dark:text-zinc-300">
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
          <label class="block text-sm font-medium text-zinc-700 dark:text-zinc-300">
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
        <p class="text-xs text-zinc-500 dark:text-zinc-400">{$t('deck.settings.unlimited_hint')}</p>
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
      <h2 class="text-lg font-semibold text-zinc-900 dark:text-zinc-100 mb-4">{$t('deck.settings.usage_heading')}</h2>
      <dl class="grid gap-3 sm:grid-cols-2 text-sm">
        <div class="flex items-center justify-between rounded-lg bg-zinc-50 dark:bg-zinc-900/60 px-3 py-2">
          <dt class="text-zinc-500 dark:text-zinc-400">{$t('deck.settings.new_used')}</dt>
          <dd class="font-medium text-zinc-900 dark:text-zinc-100" data-testid="deck-settings-new-used">{settings.new_used}</dd>
        </div>
        <div class="flex items-center justify-between rounded-lg bg-zinc-50 dark:bg-zinc-900/60 px-3 py-2">
          <dt class="text-zinc-500 dark:text-zinc-400">{$t('deck.settings.new_left')}</dt>
          <dd class="font-medium text-zinc-900 dark:text-zinc-100" data-testid="deck-settings-new-left">{leftText(settings.new_left, settings.new_unlimited)}</dd>
        </div>
        <div class="flex items-center justify-between rounded-lg bg-zinc-50 dark:bg-zinc-900/60 px-3 py-2">
          <dt class="text-zinc-500 dark:text-zinc-400">{$t('deck.settings.review_used')}</dt>
          <dd class="font-medium text-zinc-900 dark:text-zinc-100" data-testid="deck-settings-review-used">{settings.review_used}</dd>
        </div>
        <div class="flex items-center justify-between rounded-lg bg-zinc-50 dark:bg-zinc-900/60 px-3 py-2">
          <dt class="text-zinc-500 dark:text-zinc-400">{$t('deck.settings.review_left')}</dt>
          <dd class="font-medium text-zinc-900 dark:text-zinc-100" data-testid="deck-settings-review-left">{leftText(settings.review_left, settings.review_unlimited)}</dd>
        </div>
      </dl>
    </section>
  {/if}
</div>
