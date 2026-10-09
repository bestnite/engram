<script lang="ts">
  import { onMount } from 'svelte';
  import { t } from '../i18n';
  import { apiClient, ApiClientError } from '../api';
  import { authStore } from '../auth';
  import type { Deck, PackageImportReport } from '../api';
  import Select from '../components/ui/Select.svelte';
  import Checkbox from '../components/ui/Checkbox.svelte';
  import Button from '../components/ui/Button.svelte';

  // 导入来源：文件或公开 HTTPS 直链。默认文件，两条入口共用下面同一批选项。
  let source = $state<'file' | 'url'>('file');
  let decks = $state<Deck[]>([]);
  let file = $state<File | null>(null);
  let packageUrl = $state('');
  let target = $state('new_deck');
  let deckId = $state('');
  let dryRun = $state(true);
  let onConflict = $state<'skip' | 'update' | 'fail'>('update');
  let allowOthersProgress = $state(false);
  let skipMissingMedia = $state(false);
  let submitting = $state(false);
  let errorKey = $state('');
  let report = $state<PackageImportReport | null>(null);

  // 挂载标志与输入版本：卸载后不再写状态；输入版本用来判定在途结果是否已过时。
  let mounted = true;
  let requestRevision = 0;

  onMount(() => {
    void (async () => {
      try { decks = (await apiClient.getDecks()).decks; } catch { /* 列表拉取失败时退回按卡组 id 手填。 */ }
    })();
    return () => { mounted = false; };
  });

  /**
   * 来源或任一导入选项一变，上一份报告/错误就不再对应当前请求，必须清掉，否则界面会把
   * 一次旧导入的结果当成这次的选择结果展示。这里只读这些输入、不读 report/errorKey，
   * 因此写入不会反过来触发自己。同时推进输入版本：提交时冻结当时的值，之后任何改动都会让
   * 在途请求的结果作废（请求本身照旧用原负载执行，只是不再回填到界面）。
   */
  $effect(() => {
    void source; void packageUrl; void file; void target; void deckId;
    void dryRun; void onConflict; void allowOthersProgress; void skipMissingMedia;
    requestRevision++;
    report = null;
    errorKey = '';
  });

  /**
   * 本地判据：只接受可解析的 https:// 直链。缺失 URL 与不支持的协议在发请求前就拒绝，
   * 这类错误不该消耗一次出站请求，也不该依赖服务端回包才能告诉用户。
   */
  function isHttpsUrl(raw: string): boolean {
    try {
      return new URL(raw).protocol === 'https:';
    } catch {
      return false;
    }
  }

  /**
   * 把稳定 code 映射到语言包键。后端只回英文 code/message，界面绝不直接展示其英文文本；
   * URL 专用 code 与文件导入的 code 分列，超限沿用同一条「体积/配额」文案。
   */
  function importErrorKey(err: unknown): string {
    if (!(err instanceof ApiClientError)) return 'package.error.failed';
    switch (err.code) {
      case 'unauthorized':
        return 'error.unauthorized';
      case 'insufficient_role':
      case 'forbidden':
        return 'error.forbidden';
      case 'deck_import_url_invalid':
        return 'package.error.url_invalid';
      case 'deck_import_url_blocked':
        return 'package.error.url_blocked';
      case 'deck_import_url_fetch_failed':
        return 'package.error.url_fetch_failed';
      case 'deck_import_url_not_package':
        return 'package.error.url_not_package';
      case 'package_too_large':
      case 'media_quota_exceeded':
        return 'package.error.too_large';
      case 'package_unknown_kind':
      case 'package_bad_format':
      case 'package_deck_meta_invalid':
      case 'package_unsafe_entry':
      case 'package_unsafe_media':
        return 'package.error.invalid';
      default:
        return err.status === 413 ? 'package.error.too_large' : 'package.error.failed';
    }
  }

  async function submit(event: SubmitEvent): Promise<void> {
    event.preventDefault();
    if (submitting) return; // 在途请求未落地前，重复触发（连点/回车）不再发第二次。
    if (source === 'file') {
      if (!file) { errorKey = 'package.error.file_required'; return; }
    } else {
      const raw = packageUrl.trim();
      if (!raw) { errorKey = 'package.error.url_required'; return; }
      if (!isHttpsUrl(raw)) { errorKey = 'package.error.url_scheme'; return; }
    }
    if (target === 'into_deck' && !deckId) { errorKey = 'package.error.target_required'; return; }
    // 冻结本次请求的输入：负载用这里的快照发出，结果只有输入版本没变时才回填。
    const revision = requestRevision;
    const requestSource = source;
    const requestFile = file;
    const requestUrl = packageUrl.trim();
    const options = {
      target: target === 'new_deck' ? 'new_deck' : `into_deck:${deckId}`,
      dryRun,
      onConflict,
      allowOthersProgress: $authStore.user?.role === 'admin' && allowOthersProgress,
      skipMissingMedia,
    };
    submitting = true;
    errorKey = '';
    report = null;
    try {
      const result = requestSource === 'file'
        ? await apiClient.importDeckPackage(requestFile!, options)
        : await apiClient.importDeckPackageURL(requestUrl, options);
      // 输入已变（来源/选项/URL）或视图已卸载：这次结果不再对应当前选择，丢弃。
      if (!mounted || revision !== requestRevision) return;
      report = result;
    } catch (err) {
      if (!mounted || revision !== requestRevision) return;
      errorKey = importErrorKey(err);
    } finally {
      // 请求本身已结束：只要还挂载着就解除提交锁，让用户能用新选择再次提交。
      if (mounted) submitting = false;
    }
  }

  function reportRows(value: PackageImportReport): Array<[string, number | string]> {
    return [
      ['package.report.target', value.target], ['package.report.dry_run', value.dry_run ? 1 : 0],
      ['package.report.deck_id', value.deck_id ?? '—'], ['package.report.notes_created', value.notes_created],
      ['package.report.notes_updated', value.notes_updated], ['package.report.notes_skipped', value.notes_skipped],
      ['package.report.cards_created', value.cards_created], ['package.report.media_new', value.media_new],
      ['package.report.media_missing', value.media_missing], ['package.report.progress_applied', value.progress_applied],
      ['package.report.progress_skipped', value.progress_skipped], ['package.report.progress_discarded', value.progress_discarded ? 1 : 0],
      ['package.report.match_rule', value.match_rule || '—'],
    ];
  }
</script>

<div class="py-10 max-w-4xl mx-auto px-4">
  <section class="card-elevated p-6 sm:p-8 rounded-xl">
    <h1 class="text-2xl font-bold tracking-tight">{$t('package.import.title')}</h1>
    <p class="mt-2 mb-6 text-sm text-muted-foreground">{$t('package.import.intro')}</p>
    <form onsubmit={submit} class="space-y-5">
      <label class="block text-sm font-medium">{$t('package.source.label')}
        <Select
          class="mt-2"
          testId="import-source"
          value={source}
          onValueChange={(value) => (source = value as typeof source)}
          options={[{ value: 'file', label: $t('package.source.file') }, { value: 'url', label: $t('package.source.url') }]}
        />
      </label>
      {#if source === 'file'}
        <label class="block text-sm font-medium">{$t('package.import.file')}
          <!-- 校验统一交给 submit 里的本地判据：原生 required 会在真实提交时先弹浏览器自己的提示，
               与「缺失时显示语言包文案」的约定冲突，所以不设 required。 -->
          <input data-testid="import-file" class="mt-2 block w-full text-sm" type="file" accept=".edeck,.zip" onchange={(e) => file = e.currentTarget.files?.[0] ?? null} />
        </label>
      {:else}
        <label class="block text-sm font-medium">{$t('package.import.url')}
          <input
            data-testid="import-url"
            class="field-input text-sm mt-2 block w-full"
            type="text"
            inputmode="url"
            placeholder={$t('package.import.url_placeholder')}
            value={packageUrl}
            oninput={(e) => packageUrl = e.currentTarget.value}
          />
          <span class="mt-1 block text-xs text-muted-foreground">{$t('package.import.url_help')}</span>
        </label>
      {/if}
      <label class="block text-sm font-medium">{$t('package.import.target')}
        <Select
          class="mt-2"
          value={target}
          onValueChange={(value) => (target = value as typeof target)}
          options={[{ value: 'new_deck', label: $t('package.import.new_deck') }, { value: 'into_deck', label: $t('package.import.into_deck') }]}
        />
      </label>
      {#if target === 'into_deck'}
        <label class="block text-sm font-medium">{$t('package.import.deck')}
          {#if decks.length}
            <Select
              class="mt-2"
              bind:value={deckId}
              placeholder={$t('package.import.choose_deck')}
              options={decks.map((deck) => ({ value: String(deck.id), label: deck.name }))}
            />
          {:else}
            <input class="field-input text-sm mt-2 block w-full" type="text" bind:value={deckId} />
          {/if}
        </label>
      {/if}
      <label class="block text-sm font-medium">{$t('package.import.conflict')}
        <Select
          class="mt-2"
          testId="package-import-conflict"
          value={onConflict}
          onValueChange={(value) => (onConflict = value as typeof onConflict)}
          options={[{ value: 'update', label: $t('package.import.update') }, { value: 'skip', label: $t('package.import.skip') }, { value: 'fail', label: $t('package.import.fail') }]}
        />
      </label>
      <div class="space-y-3 text-sm">
        <label class="flex items-center gap-2"><Checkbox bind:checked={dryRun} label={$t('package.import.dry_run')} />{$t('package.import.dry_run')}</label>
        {#if $authStore.user?.role === 'admin'}<label class="flex items-center gap-2"><Checkbox bind:checked={allowOthersProgress} label={$t('package.import.allow_progress')} />{$t('package.import.allow_progress')}</label>{/if}
        <label class="flex items-center gap-2"><Checkbox bind:checked={skipMissingMedia} label={$t('package.import.skip_media')} />{$t('package.import.skip_media')}</label>
      </div>
      {#if errorKey}<p role="alert" class="text-sm text-rose-700 dark:text-rose-400">{$t(errorKey)}</p>{/if}
      <Button type="submit" testId="import-submit" disabled={submitting}>{$t(submitting ? 'package.import.submitting' : 'package.import.submit')}</Button>
    </form>
  </section>
  {#if report}
    <section class="card-elevated mt-6 p-6 rounded-xl" aria-live="polite">
      <h2 class="text-lg font-semibold">{$t('package.report.heading')}</h2>
      <dl class="mt-4 grid gap-3 sm:grid-cols-2 text-sm">
        {#each reportRows(report) as [key, value] (key)}<div class="flex justify-between gap-3 border-b border-border pb-2"><dt class="text-muted-foreground">{$t(key)}</dt><dd class="font-medium">{key.endsWith('dry_run') || key.endsWith('progress_discarded') ? $t(value ? 'package.value.yes' : 'package.value.no') : value}</dd></div>{/each}
      </dl>
      {#if report.errors.length}<h3 class="mt-5 font-semibold">{$t('package.report.errors')}</h3><ul class="mt-2 list-disc pl-5 text-sm">{#each report.errors as item}<li>{item.entry}: {item.reason}</li>{/each}</ul>{/if}
      {#if report.dry_run}<p class="mt-4 text-sm text-amber-700 dark:text-amber-400">{$t('package.report.preview_notice')}</p>{/if}
    </section>
  {/if}
</div>
