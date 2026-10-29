<script lang="ts">
  import { onMount } from 'svelte';
  import { t } from '../i18n';
  import { apiClient, ApiClientError } from '../api';
  import { authStore } from '../auth';
  import type { Deck, PackageImportReport } from '../api';
  import Select from '../components/ui/Select.svelte';
  import Checkbox from '../components/ui/Checkbox.svelte';
  import Button from '../components/ui/Button.svelte';
  import Page from '../components/ui/Page.svelte';
  import PageHeader from '../components/ui/PageHeader.svelte';
  import Dropzone from '../components/ui/Dropzone.svelte';
  import SegmentedControl from '../components/ui/SegmentedControl.svelte';
  import { toast } from '../components/ui/toast';

  // Import source: a file or a public HTTPS direct link. File by default; both entries share the same set of options below.
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
  let applyWeights = $state(false);
  let submitting = $state(false);
  // Only holds local validation errors from before sending the request; request failures go through toast.
  let errorKey = $state('');
  let report = $state<PackageImportReport | null>(null);
  let reportSection = $state<HTMLElement | null>(null);

  // Mount flag and input version: state is no longer written after unmount; the input version decides whether an in-flight result is already stale.
  let mounted = true;
  let requestRevision = 0;

  onMount(() => {
    void (async () => {
      try { decks = (await apiClient.getDecks()).decks; } catch { /* Falls back to entering the deck id by hand when the list fetch fails. */ }
    })();
    return () => { mounted = false; };
  });

  /**
   * Once the source or any import option changes, the previous report/error no longer corresponds to the current
   * request and must be cleared, otherwise the UI would present an old import's result as the outcome of this selection.
   * This only reads those inputs and not report/errorKey, so the write cannot trigger itself in turn. It also advances
   * the input version: submission freezes the values at that moment, and any later change invalidates the result of the
   * in-flight request (the request itself still runs with the original payload, it is just no longer written back to the UI).
   */
  $effect(() => {
    void source; void packageUrl; void file; void target; void deckId;
    void dryRun; void onConflict; void allowOthersProgress; void skipMissingMedia; void applyWeights;
    requestRevision++;
    report = null;
    errorKey = '';
  });

  /**
   * Local criterion: only a parseable https:// direct link is accepted. A missing URL and an unsupported scheme are
   * rejected before the request is sent; such errors should not consume an outbound request, nor depend on a server
   * response before the user can be told.
   */
  function isHttpsUrl(raw: string): boolean {
    try {
      return new URL(raw).protocol === 'https:';
    } catch {
      return false;
    }
  }

  /**
   * Map the stable code to a language pack key. The backend only returns an English code/message, and the UI never
   * shows its English text directly; URL-only codes and file import codes are listed separately, and the over-limit
   * case reuses the same "size/quota" copy.
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
    if (submitting) return; // Until the in-flight request lands, repeated triggering (double click/Enter) does not send a second one.
    if (source === 'file') {
      if (!file) { errorKey = 'package.error.file_required'; return; }
    } else {
      const raw = packageUrl.trim();
      if (!raw) { errorKey = 'package.error.url_required'; return; }
      if (!isHttpsUrl(raw)) { errorKey = 'package.error.url_scheme'; return; }
    }
    if (target === 'into_deck' && !deckId) { errorKey = 'package.error.target_required'; return; }
    // Freeze this request's inputs: the payload is sent from this snapshot, and the result is only written back when the input version is unchanged.
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
      // The in-package weight is only used for a newly created deck's preset; the server ignores it when merging into an existing deck, so no checkbox is sent from here either.
      applyWeights: target === 'new_deck' && applyWeights,
    };
    submitting = true;
    errorKey = '';
    report = null;
    try {
      const result = requestSource === 'file'
        ? await apiClient.importDeckPackage(requestFile!, options)
        : await apiClient.importDeckPackageURL(requestUrl, options);
      // The inputs changed (source/options/URL) or the view unmounted: this result no longer matches the current selection, discard it.
      if (!mounted || revision !== requestRevision) return;
      report = result;
      notifyResult(result);
    } catch (err) {
      if (!mounted || revision !== requestRevision) return;
      toast.error($t(importErrorKey(err)));
    } finally {
      // The request itself has finished: as long as it is still mounted, release the submit lock so the user can submit again with a new selection.
      if (mounted) submitting = false;
    }
  }

  /**
   * The report area sits below the form, and in a long form the user will not see the result without scrolling down, so
   * a light toast is raised once it lands successfully, with a button that jumps to the report area. When some entries
   * were not imported it uses the warning style, so the user does not take a partial failure for complete success.
   */
  function notifyResult(value: PackageImportReport): void {
    const message = value.dry_run
      ? $t('package.toast.dry_run_done')
      : $t('package.toast.import_done', { created: value.notes_created, updated: value.notes_updated });
    const options = {
      description: value.errors.length ? $t('package.toast.entries_failed', { count: value.errors.length }) : undefined,
      action: {
        label: $t('package.toast.view_report'),
        onClick: () => reportSection?.scrollIntoView({ behavior: 'smooth', block: 'start' }),
      },
    };
    if (value.errors.length) toast.warning(message, options);
    else toast.success(message, options);
  }

  function targetLabel(raw: string): string {
    if (raw === 'new_deck') return $t('package.import.new_deck');
    // When merging into an existing deck the server writes into_deck:<deck id>, and the deck id is shown on a separate line.
    if (raw === 'into_deck' || raw.startsWith('into_deck:')) return $t('package.import.into_deck');
    return raw;
  }

  function reportRows(value: PackageImportReport): Array<[string, number | string]> {
    return [
      // The target is the server's stable value (new_deck / into_deck); for display it is swapped for the same copy as the form options.
      ['package.report.target', targetLabel(value.target)], ['package.report.dry_run', value.dry_run ? 1 : 0],
      ['package.report.deck_id', value.deck_id ?? '—'], ['package.report.notes_created', value.notes_created],
      ['package.report.notes_updated', value.notes_updated], ['package.report.notes_skipped', value.notes_skipped],
      // The cards generated from those notes are not listed: the interface calls a note a card, so a second
      // "cards" count would read as the same thing counted twice. The API still reports cards_created.
      ['package.report.media_new', value.media_new],
      ['package.report.media_missing', value.media_missing], ['package.report.progress_applied', value.progress_applied],
      ['package.report.progress_skipped', value.progress_skipped], ['package.report.progress_discarded', value.progress_discarded ? 1 : 0],
      ['package.report.weights_applied', value.weights_applied ? 1 : 0], ['package.report.weights_discarded', value.weights_discarded ? 1 : 0],
      ['package.report.match_rule', value.match_rule || '—'],
    ];
  }
</script>

<Page>
  <PageHeader title={$t('package.import.title')} description={$t('package.import.intro')} />
  <form onsubmit={submit} class="space-y-6">
    <div class="space-y-2">
      <span class="block text-sm font-medium" id="import-source-label">{$t('package.source.label')}</span>
      <SegmentedControl
        testId="import-source"
        ariaLabel={$t('package.source.label')}
        value={source}
        onValueChange={(value) => (source = value as typeof source)}
        options={[{ value: 'file', label: $t('package.source.file') }, { value: 'url', label: $t('package.source.url') }]}
      />
    </div>
    {#if source === 'file'}
      <div class="space-y-2">
        <span class="block text-sm font-medium">{$t('package.import.file')}</span>
        <!-- Validation is left entirely to the local criterion in submit: the native required would make the browser show its
             own prompt on a real submit, conflicting with the "show the language pack copy when missing" convention, so
             required is not set. -->
        <Dropzone bind:file testId="import-file" accept=".edeck,.zip" hint={$t('dropzone.import_hint')} />
      </div>
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
        <span class="mt-1.5 block text-xs font-normal text-muted-foreground">{$t('package.import.url_help')}</span>
      </label>
    {/if}
    <div class="grid gap-5 sm:grid-cols-2 lg:grid-cols-3">
      <label class="block text-sm font-medium">{$t('package.import.target')}
        <Select
          class="mt-2"
          testId="package-import-target"
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
    </div>
    <div class="space-y-3 text-sm">
      <label class="flex items-center gap-2"><Checkbox bind:checked={dryRun} label={$t('package.import.dry_run')} />{$t('package.import.dry_run')}</label>
      {#if $authStore.user?.role === 'admin'}<label class="flex items-center gap-2"><Checkbox bind:checked={allowOthersProgress} label={$t('package.import.allow_progress')} />{$t('package.import.allow_progress')}</label>{/if}
      <label class="flex items-center gap-2"><Checkbox bind:checked={skipMissingMedia} label={$t('package.import.skip_media')} />{$t('package.import.skip_media')}</label>
      {#if target === 'new_deck'}<label class="flex items-center gap-2"><Checkbox bind:checked={applyWeights} testId="package-import-apply-weights" label={$t('package.import.apply_weights')} />{$t('package.import.apply_weights')}</label>{/if}
    </div>
    <div class="flex flex-wrap items-center gap-3 border-t border-border pt-5">
      <Button type="submit" size="lg" testId="import-submit" loading={submitting}>{$t(dryRun ? 'package.import.submit_dry' : 'package.import.submit')}</Button>
      {#if errorKey}<p role="alert" class="text-sm text-destructive-foreground">{$t(errorKey)}</p>{/if}
    </div>
  </form>
  {#if report}
    <section bind:this={reportSection} class="mt-10 scroll-mt-6 border-t border-border pt-6 animate-in fade-in-0 slide-in-from-bottom-1 duration-200" aria-live="polite">
      <h2 class="text-base font-semibold">{$t('package.report.heading')}</h2>
      <dl class="mt-4 grid gap-x-8 sm:grid-cols-2 lg:grid-cols-3 text-sm">
        {#each reportRows(report) as [key, value] (key)}<div class="flex justify-between gap-3 border-b border-border py-2"><dt class="text-muted-foreground">{$t(key)}</dt><dd class="font-medium tabular-nums">{key.endsWith('dry_run') || key.endsWith('progress_discarded') ? $t(value ? 'package.value.yes' : 'package.value.no') : value}</dd></div>{/each}
      </dl>
      {#if report.errors.length}<h3 class="mt-5 text-sm font-semibold">{$t('package.report.errors')}</h3><ul class="mt-2 list-disc pl-5 text-sm">{#each report.errors as item}<li>{item.entry}: {item.reason}</li>{/each}</ul>{/if}
      {#if report.dry_run}<p class="mt-4 text-sm text-warning">{$t('package.report.preview_notice')}</p>{/if}
    </section>
  {/if}
</Page>
