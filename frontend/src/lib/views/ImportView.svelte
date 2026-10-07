<script lang="ts">
  import { onMount } from 'svelte';
  import { t } from '../i18n';
  import { apiClient, ApiClientError } from '../api';
  import { authStore } from '../auth';
  import type { Deck, PackageImportReport } from '../api';
  import Select from '../components/ui/Select.svelte';

  let decks = $state<Deck[]>([]);
  let file = $state<File | null>(null);
  let target = $state('new_deck');
  let deckId = $state('');
  let dryRun = $state(true);
  let onConflict = $state<'skip' | 'update' | 'fail'>('update');
  let allowOthersProgress = $state(false);
  let skipMissingMedia = $state(false);
  let submitting = $state(false);
  let errorKey = $state('');
  let report = $state<PackageImportReport | null>(null);

  onMount(async () => {
    try { decks = (await apiClient.getDecks()).decks; } catch { /* Failed listing falls back to selecting a deck by ID. */ }
  });

  async function submit(event: SubmitEvent): Promise<void> {
    event.preventDefault();
    if (!file) { errorKey = 'package.error.file_required'; return; }
    if (target === 'into_deck' && !deckId) { errorKey = 'package.error.target_required'; return; }
    submitting = true;
    errorKey = '';
    report = null;
    try {
      report = await apiClient.importDeckPackage(file, {
        target: target === 'new_deck' ? 'new_deck' : `into_deck:${deckId}`,
        dryRun,
        onConflict,
        allowOthersProgress: $authStore.user?.role === 'admin' && allowOthersProgress,
        skipMissingMedia,
      });
    } catch (err) {
      errorKey = err instanceof ApiClientError
        ? err.code === 'unauthorized' ? 'error.unauthorized'
          : err.code === 'insufficient_role' || err.code === 'forbidden' ? 'error.forbidden'
            : err.code === 'package_unknown_kind' || err.code === 'package_bad_format' || err.code === 'package_deck_meta_invalid' || err.code === 'package_unsafe_entry' || err.code === 'package_unsafe_media' ? 'package.error.invalid'
              : err.code === 'package_too_large' || err.code === 'media_quota_exceeded' || err.status === 413 ? 'package.error.too_large'
              : 'package.error.failed'
        : 'package.error.failed';
    } finally {
      submitting = false;
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
    <p class="mt-2 mb-6 text-sm text-zinc-600 dark:text-zinc-400">{$t('package.import.intro')}</p>
    <form onsubmit={submit} class="space-y-5">
      <label class="block text-sm font-medium">{$t('package.import.file')}
        <input class="mt-2 block w-full text-sm" type="file" accept=".edeck,.zip" required onchange={(e) => file = e.currentTarget.files?.[0] ?? null} />
      </label>
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
            <input class="mt-2 block w-full rounded-md border border-zinc-300 dark:border-zinc-700 bg-white dark:bg-zinc-900 px-3 py-2" type="number" min="1" bind:value={deckId} />
          {/if}
        </label>
      {/if}
      <label class="block text-sm font-medium">{$t('package.import.conflict')}
        <Select
          class="mt-2"
          value={onConflict}
          onValueChange={(value) => (onConflict = value as typeof onConflict)}
          options={[{ value: 'update', label: $t('package.import.update') }, { value: 'skip', label: $t('package.import.skip') }, { value: 'fail', label: $t('package.import.fail') }]}
        />
      </label>
      <div class="space-y-3 text-sm">
        <label class="flex items-center gap-2"><input type="checkbox" bind:checked={dryRun} />{$t('package.import.dry_run')}</label>
        {#if $authStore.user?.role === 'admin'}<label class="flex items-center gap-2"><input type="checkbox" bind:checked={allowOthersProgress} />{$t('package.import.allow_progress')}</label>{/if}
        <label class="flex items-center gap-2"><input type="checkbox" bind:checked={skipMissingMedia} />{$t('package.import.skip_media')}</label>
      </div>
      {#if errorKey}<p role="alert" class="text-sm text-rose-700 dark:text-rose-400">{$t(errorKey)}</p>{/if}
      <button type="submit" disabled={submitting} class="px-4 py-2 rounded-lg bg-zinc-900 text-white dark:bg-zinc-100 dark:text-zinc-900 disabled:opacity-50">{$t(submitting ? 'package.import.submitting' : 'package.import.submit')}</button>
    </form>
  </section>
  {#if report}
    <section class="card-elevated mt-6 p-6 rounded-xl" aria-live="polite">
      <h2 class="text-lg font-semibold">{$t('package.report.heading')}</h2>
      <dl class="mt-4 grid gap-3 sm:grid-cols-2 text-sm">
        {#each reportRows(report) as [key, value] (key)}<div class="flex justify-between gap-3 border-b border-zinc-200 dark:border-zinc-800 pb-2"><dt class="text-zinc-600 dark:text-zinc-400">{$t(key)}</dt><dd class="font-medium">{key.endsWith('dry_run') || key.endsWith('progress_discarded') ? $t(value ? 'package.value.yes' : 'package.value.no') : value}</dd></div>{/each}
      </dl>
      {#if report.errors.length}<h3 class="mt-5 font-semibold">{$t('package.report.errors')}</h3><ul class="mt-2 list-disc pl-5 text-sm">{#each report.errors as item}<li>{item.entry}: {item.reason}</li>{/each}</ul>{/if}
      {#if report.dry_run}<p class="mt-4 text-sm text-amber-700 dark:text-amber-400">{$t('package.report.preview_notice')}</p>{/if}
    </section>
  {/if}
</div>
