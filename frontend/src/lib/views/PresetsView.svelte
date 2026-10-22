<script lang="ts">
  import { onMount, onDestroy } from 'svelte';
  import { t } from '../i18n';
  import { apiClient, ApiClientError } from '../api';
  import Dialog from '../components/ui/Dialog.svelte';
  import Button from '../components/ui/Button.svelte';
  import { toast } from '../components/ui/toast';
  import Checkbox from '../components/ui/Checkbox.svelte';
  import HelpTip from '../components/ui/HelpTip.svelte';
  import Skeleton from '../components/ui/Skeleton.svelte';
  import Page from '../components/ui/Page.svelte';
  import PageHeader from '../components/ui/PageHeader.svelte';
  import { Pencil, Plus, RefreshCw, RotateCcw, Sparkles, Trash2 } from '@lucide/svelte';
  import { presetDisplayName } from '../labels';
  import type {
    PresetsResponse,
    PresetRecord,
    PresetJob,
    PresetWriteRequest,
    OptimizeGate,
    Deck,
  } from '../api';

  interface Props {
    initialLoading?: boolean;
    initialError?: ApiClientError | Error | null;
    initialData?: PresetsResponse | null;
  }

  let {
    initialLoading = true,
    initialError = null,
    initialData = null,
  }: Props = $props();

  // svelte-ignore state_referenced_locally
  let loading = $state(initialLoading);
  // svelte-ignore state_referenced_locally
  let loadError = $state<ApiClientError | Error | null>(initialError);
  // svelte-ignore state_referenced_locally
  let data = $state<PresetsResponse | null>(initialData);

  // The create/edit dialog shares one form; formMode decides whether it submits to create or update.
  let formOpen = $state(false);
  let formMode = $state<'create' | 'edit'>('create');
  let formTargetId = $state<string | null>(null);
  let formName = $state('');
  let formRetention = $state('0.90');
  let formLearning = $state('1m,10m');
  let formRelearning = $state('10m');
  let formMaxInterval = $state('36500');
  let formFuzz = $state(true);
  let formSaving = $state(false);
  let formError = $state('');
  // The default preset's name is its identity (the server finds it by name), so that field is read-only while editing:
  // making it editable would write a state where "the default preset is no longer called Default", and the server would then add another one under the same name.
  let formNameLocked = $state(false);

  // In-flight preset operation: id decides which row is disabled, action decides which button on that row spins.
  // Operation results go through toast; gate's "N more to go" is persistent state and is rendered separately by the gate.
  let busy = $state<{ id: string; action: 'optimize' | 'revert' } | null>(null);
  const busyId = $derived(busy?.id ?? null);

  // Linked decks and preset delete state
  let decks = $state<Deck[]>([]);
  let presetToDelete = $state<PresetRecord | null>(null);
  let deletingPreset = $state(false);
  let deletePresetError = $state('');

  let pollTimer: ReturnType<typeof setInterval> | null = null;
  let pollPresetId: string | null = null;
  let pollJobId: string | null = null;

  const presets = $derived(data ? data.presets : []);

  /** Read the preset list and the optimization gate; the server fills in the default preset. */
  async function load(): Promise<void> {
    loading = true;
    loadError = null;
    try {
      const [pRes, dRes] = await Promise.all([
        apiClient.listPresets(),
        apiClient.getDecks().catch(() => ({ decks: [] })),
      ]);
      data = pRes;
      decks = dRes.decks;
      resumePolling();
    } catch (err) {
      loadError = err instanceof Error ? err : new Error(String(err));
    } finally {
      loading = false;
    }
  }

  function getPresetDecks(presetId: string): Deck[] {
    return decks.filter((d) => d.preset_id === presetId);
  }

  function promptDeletePreset(p: PresetRecord): void {
    presetToDelete = p;
    deletePresetError = '';
  }

  async function confirmDeletePreset(): Promise<void> {
    if (!presetToDelete || deletingPreset) return;
    deletingPreset = true;
    deletePresetError = '';
    try {
      const nextData = await apiClient.deletePreset(presetToDelete.id);
      data = nextData;
      presetToDelete = null;
    } catch (err) {
      if (err instanceof ApiClientError && err.code === 'preset_in_use') {
        deletePresetError = 'presets.delete.in_use';
      } else {
        deletePresetError = 'presets.delete.failed';
      }
    } finally {
      deletingPreset = false;
    }
  }

  /** Resume polling when a job is in flight after a refresh, so the state of a running task is not lost to the refresh. */
  function resumePolling(): void {
    if (!data) return;
    const active = data.presets.find((p) => p.job && isActiveJob(p.job));
    if (active && active.job) {
      startPolling(active.id, active.job.id);
    }
  }

  /** Only non-negative integers are accepted for the maximum interval; the regex rejects decimals and non-digits first. */
  function validSteps(spec: string): boolean {
    const trimmed = spec.trim();
    if (trimmed === '') return true;
    for (const rawPart of trimmed.split(',')) {
      const part = rawPart.trim();
      if (part === '') continue;
      const match = part.match(/^(\d+\.?\d*|\.\d+)(m|min|s|sec|h|hr|d)?$/i);
      if (!match) return false;
      if (!(Number(match[1]) > 0)) return false;
    }
    return true;
  }

  /** Parse the form and validate the same way the backend does; returning an i18n key string signals rejection. */
  function parseForm(): PresetWriteRequest | string {
    const name = formName.trim();
    if (name === '') return 'presets.form.error.name_required';
    const retention = Number(formRetention.trim());
    if (!Number.isFinite(retention) || retention <= 0 || retention >= 1) {
      return 'presets.form.error.retention';
    }
    const maxInterval = Number(formMaxInterval.trim());
    if (!Number.isInteger(maxInterval) || maxInterval <= 0) {
      return 'presets.form.error.max_interval';
    }
    const learning = formLearning.trim();
    const relearning = formRelearning.trim();
    if (!validSteps(learning) || !validSteps(relearning)) {
      return 'presets.form.error.steps';
    }
    return {
      name,
      desired_retention: retention,
      learning_steps: learning,
      relearning_steps: relearning,
      maximum_interval_days: maxInterval,
      enable_fuzz: formFuzz,
    };
  }

  function openCreate(): void {
    formMode = 'create';
    formTargetId = null;
    formName = '';
    formNameLocked = false;
    formRetention = '0.90';
    formLearning = '1m,10m';
    formRelearning = '10m';
    formMaxInterval = '36500';
    formFuzz = true;
    formError = '';
    formOpen = true;
  }

  function openEdit(p: PresetRecord): void {
    formMode = 'edit';
    formTargetId = p.id;
    formName = p.name;
    formNameLocked = p.is_default;
    formRetention = p.desired_retention.toFixed(2);
    formLearning = p.learning_steps;
    formRelearning = p.relearning_steps;
    formMaxInterval = String(p.maximum_interval_days);
    formFuzz = p.enable_fuzz;
    formError = '';
    formOpen = true;
  }

  function closeForm(): void {
    formOpen = false;
    formError = '';
  }

  async function submitForm(event: SubmitEvent): Promise<void> {
    event.preventDefault();
    formError = '';
    const parsed = parseForm();
    if (typeof parsed === 'string') {
      formError = parsed;
      return;
    }
    formSaving = true;
    try {
      if (formMode === 'create') {
        data = await apiClient.createPreset(parsed);
      } else if (formTargetId !== null) {
        data = await apiClient.updatePreset(formTargetId, parsed);
      }
      closeForm();
    } catch (err) {
      formError = writeErrorKey(err);
    } finally {
      formSaving = false;
    }
  }

  /** Trigger optimization: polling starts on success; when below the gate or a job already exists, refresh the gate and show one hint. */
  async function runOptimize(p: PresetRecord): Promise<void> {
    busy = { id: p.id, action: 'optimize' };
    try {
      const resp = await apiClient.optimizePreset(p.id);
      applyJob(p.id, resp.job, resp.gate);
      if (resp.job && isActiveJob(resp.job)) {
        startPolling(p.id, resp.job.id);
      }
    } catch (err) {
      if (err instanceof ApiClientError && err.code === 'insufficient_reviews') {
        // Below the gate: the gate paragraph renders "N more to go" on refresh, so no extra hint is needed.
        await load();
      } else if (err instanceof ApiClientError && err.code === 'optimize_conflict') {
        await load();
        toast.warning($t('presets.optimize.conflict'));
      } else {
        toast.error($t('presets.error.failed'));
      }
    } finally {
      busy = null;
    }
  }

  /** One-click revert to the default weights; the response is the whole list, and the weight source returns to default with it. */
  async function revertWeights(p: PresetRecord): Promise<void> {
    busy = { id: p.id, action: 'revert' };
    try {
      data = await apiClient.revertPresetWeights(p.id);
    } catch (err) {
      toast.error($t(optimizationErrorKey(err)));
    } finally {
      busy = null;
    }
  }

  function startPolling(presetId: string, jobId: string): void {
    stopPolling();
    pollPresetId = presetId;
    pollJobId = jobId;
    pollTimer = setInterval(pollOnce, 2000);
  }

  function stopPolling(): void {
    if (pollTimer !== null) {
      clearInterval(pollTimer);
      pollTimer = null;
    }
    pollPresetId = null;
    pollJobId = null;
  }

  async function pollOnce(): Promise<void> {
    if (pollPresetId === null || pollJobId === null) return;
    const presetId = pollPresetId;
    const jobId = pollJobId;
    try {
      const resp = await apiClient.getPresetOptimizeStatus(presetId, jobId);
      applyJob(presetId, resp.job, resp.gate);
      if (!resp.job || !isActiveJob(resp.job)) {
        stopPolling();
        // Refresh the whole list in place once done: the weight source and the result summary always come from the same database read.
        await load();
      }
    } catch {
      // Polling failures are not silent: show one error hint and stop, so the user can retry manually.
      toast.error($t('presets.error.failed'));
      stopPolling();
    }
  }

  /** Update one preset's job and gate in place, without touching the other presets. */
  function applyJob(presetId: string, job: PresetJob | null, nextGate: OptimizeGate): void {
    if (!data) return;
    data = {
      presets: data.presets.map((p) => (p.id === presetId ? { ...p, job, gate: nextGate } : p)),
    };
  }

  function writeErrorKey(err: unknown): string {
    if (err instanceof ApiClientError) {
      switch (err.code) {
        case 'preset_name_required':
          return 'presets.form.error.name_required';
        case 'preset_retention_invalid':
          return 'presets.form.error.retention';
        case 'preset_max_interval_invalid':
          return 'presets.form.error.max_interval';
        case 'preset_steps_invalid':
          return 'presets.form.error.steps';
        case 'invalid_request':
          return 'presets.form.error.invalid_request';
        case 'preset_default_protected':
          return 'presets.form.error.default_protected';
        case 'not_found':
          return 'presets.error.not_found';
        default:
          return 'presets.form.error.save_failed';
      }
    }
    return 'presets.form.error.save_failed';
  }

  function optimizationErrorKey(err: unknown): string {
    if (err instanceof ApiClientError && err.code === 'not_found') return 'presets.error.not_found';
    return 'presets.error.failed';
  }

  function loadErrorKey(): string {
    if (loadError instanceof ApiClientError) {
      if (loadError.isUnauthorized) return 'error.unauthorized';
      if (loadError.isForbidden) return 'error.forbidden';
      if (loadError.isNotFound) return 'error.not_found';
    }
    return 'presets.failed';
  }

  function weightsSourceKey(p: PresetRecord): string {
    return p.weights_optimized ? 'presets.weights.source_optimized' : 'presets.weights.source_default';
  }

  function fuzzValueKey(p: PresetRecord): string {
    return p.enable_fuzz ? 'presets.fuzz_on' : 'presets.fuzz_off';
  }

  function isActiveJob(job: PresetJob): boolean {
    return job.status === 'queued' || job.status === 'running';
  }

  function jobStatusKey(status: string): string {
    switch (status) {
      case 'queued':
        return 'presets.optimize.queued';
      case 'running':
        return 'presets.optimize.running';
      case 'succeeded':
        return 'presets.optimize.succeeded';
      case 'failed':
        return 'presets.optimize.failed';
      default:
        return 'presets.optimize.queued';
    }
  }

  function stageKey(stage: string): string {
    switch (stage) {
      case 'read_logs':
        return 'presets.optimize.stage.read_logs';
      case 'training':
        return 'presets.optimize.stage.training';
      case 'writing':
        return 'presets.optimize.stage.writing';
      default:
        return 'presets.optimize.stage.unknown';
    }
  }

  function verdictKey(verdict: string): string {
    switch (verdict) {
      case 'improved':
        return 'presets.optimize.improved';
      case 'not_improved':
        return 'presets.optimize.not_improved';
      case 'insufficient_sample':
        return 'presets.optimize.sample_insufficient';
      default:
        return '';
    }
  }

  /** Render the before/after and the conclusion only when both sides have enough metric samples. */
  function showsFit(verdict: string): boolean {
    return verdict === 'improved' || verdict === 'not_improved';
  }

  function fit(labelKey: string, logLoss: number, rmse: number): string {
    return $t(labelKey, { logloss: logLoss.toFixed(4), rmse: rmse.toFixed(4) });
  }

  onMount(() => {
    if (initialData === null && initialError === null) {
      load();
    }
  });

  onDestroy(stopPolling);
</script>

<Page testId="presets-view">
  <PageHeader title={$t('presets.heading')} testId="presets-heading">
    {#snippet actions()}
      <Button type="button" testId="presets-new" onclick={openCreate} variant="primary" size="lg">
        <Plus class="size-4" aria-hidden="true" />
        {$t('presets.new.button')}
      </Button>
    {/snippet}
  </PageHeader>

  {#if loading}
    <Skeleton testId="presets-loading" label={$t('common.loading')} />
  {:else if loadError}
    <div data-testid="presets-failed" class="py-16 text-center">
      <p role="alert" class="text-base font-medium text-foreground">{$t(loadErrorKey())}</p>
      <Button type="button" testId="presets-retry" onclick={load} variant="outline" size="lg" class="mt-4">
        <RefreshCw class="size-4" aria-hidden="true" />
        <span>{$t('common.retry')}</span>
      </Button>
    </div>
  {:else if data && presets.length === 0}
    <div data-testid="presets-empty" class="rounded-lg border border-dashed border-border py-16 text-center text-sm text-muted-foreground">
      {$t('presets.empty')}
    </div>
  {:else if data}
    <div class="space-y-4" data-testid="presets-list">
      {#each presets as p (p.id)}
        {@const usedBy = getPresetDecks(p.id)}
        {@const progress = p.gate.min > 0 ? Math.min(1, p.gate.reviews / p.gate.min) : 1}
        <!-- Each preset has only this one border; internal sections use dividers instead of cards nested in cards. -->
        <article data-testid={`preset-${p.id}`} class="overflow-hidden rounded-lg border border-border">
          <header class="flex flex-wrap items-start justify-between gap-x-6 gap-y-3 px-5 py-4">
            <div class="min-w-0">
              <h2 class="text-base font-semibold text-foreground" data-testid={`preset-${p.id}-name`}>
                {presetDisplayName(p.name, p.is_default, $t)}
              </h2>
              <p class="mt-1 text-[13px] text-muted-foreground">
                {$t('presets.used_by_decks')}
                {#if usedBy.length > 0}
                  {#each usedBy as d, index (d.id)}<a href={`/decks/${d.id}`} class="text-foreground/80 underline-offset-2 hover:text-foreground hover:underline">{d.name}</a>{#if index < usedBy.length - 1}<span aria-hidden="true">、</span>{/if}{/each}
                {:else}
                  <span>{$t('presets.unused')}</span>
                {/if}
              </p>
            </div>
            <div class="flex flex-wrap items-center gap-2">
              <Button type="button" variant="outline" size="sm" testId={`preset-${p.id}-edit`} onclick={() => openEdit(p)}>
                <Pencil class="size-3.5" aria-hidden="true" />
                {$t('presets.edit.button')}
              </Button>
              <Button
                type="button"
                variant="outline"
                size="sm"
                testId={`preset-${p.id}-optimize`}
                loading={busy?.id === p.id && busy.action === 'optimize'}
                disabled={busyId === p.id || !p.gate.eligible}
                title={!p.gate.eligible ? $t('presets.optimize.gate_shortfall', { count: p.gate.shortfall }) : ''}
                onclick={() => runOptimize(p)}
              >
                <Sparkles class="size-3.5" aria-hidden="true" />
                {$t('presets.optimize.button')}
              </Button>
              <Button
                type="button"
                variant="ghost"
                size="sm"
                testId={`preset-${p.id}-revert`}
                loading={busy?.id === p.id && busy.action === 'revert'}
                disabled={busyId === p.id}
                title={$t('presets.revert.note')}
                onclick={() => revertWeights(p)}
              >
                <RotateCcw class="size-3.5" aria-hidden="true" />
                {$t('presets.revert.button')}
              </Button>
              {#if !p.is_default}
                <Button
                  type="button"
                  variant="ghost"
                  size="icon"
                  class="size-8 hover:bg-destructive-soft hover:text-destructive-foreground"
                  testId={`preset-${p.id}-delete`}
                  disabled={busyId === p.id || usedBy.length > 0}
                  label={$t('presets.delete.action')}
                  title={usedBy.length > 0 ? $t('presets.delete.in_use') : $t('presets.delete.action')}
                  onclick={() => promptDeletePreset(p)}
                >
                  <Trash2 class="size-4" aria-hidden="true" />
                </Button>
              {/if}
            </div>
          </header>

          <!-- Parameters: one metric band separated by vertical bars. -->
          <dl class="grid grid-cols-2 border-t border-border sm:grid-cols-4 sm:divide-x sm:divide-border">
            <div class="px-5 py-3 max-sm:border-b max-sm:border-r max-sm:border-border">
              <dt class="text-xs text-muted-foreground">{$t('presets.retention_label')}</dt>
              <dd class="mt-0.5 text-sm font-semibold tabular-nums" data-testid={`preset-${p.id}-retention`}>{p.desired_retention.toFixed(2)}</dd>
            </div>
            <div class="px-5 py-3 max-sm:border-b max-sm:border-border">
              <dt class="text-xs text-muted-foreground">{$t('presets.max_interval_label')}</dt>
              <dd class="mt-0.5 text-sm font-semibold tabular-nums" data-testid={`preset-${p.id}-max-interval`}>{p.maximum_interval_days}</dd>
            </div>
            <div class="px-5 py-3 max-sm:border-r max-sm:border-border">
              <dt class="text-xs text-muted-foreground">{$t('presets.fuzz_label')}</dt>
              <dd class="mt-0.5 text-sm font-semibold" data-testid={`preset-${p.id}-fuzz`}>{$t(fuzzValueKey(p))}</dd>
            </div>
            <div class="px-5 py-3">
              <dt class="text-xs text-muted-foreground">{$t('presets.weights.heading')}</dt>
              <dd class="mt-0.5 text-sm font-semibold" data-testid={`preset-${p.id}-weights-source`}>{$t(weightsSourceKey(p))}</dd>
            </div>
          </dl>

          {#if p.weights_optimized_at || p.weights_review_count !== null || p.weights_raw}
            <div class="space-y-1 border-t border-border px-5 py-3 text-[13px] text-muted-foreground">
              <p>
                {#if p.weights_optimized_at}{$t('presets.weights.optimized_at', { time: p.weights_optimized_at })}{/if}
                {#if p.weights_optimized_at && p.weights_review_count !== null}<span aria-hidden="true"> · </span>{/if}
                {#if p.weights_review_count !== null}{$t('presets.weights.review_count', { count: p.weights_review_count })}{/if}
              </p>
              {#if p.weights_raw}
                <details>
                  <summary class="cursor-pointer text-brand">{$t('presets.weights.raw_label')}</summary>
                  <code class="mt-2 block max-h-32 overflow-auto rounded-md bg-surface p-3 font-mono text-[11px] break-all text-foreground/80" data-testid={`preset-${p.id}-weights-raw`}>{p.weights_raw}</code>
                </details>
              {/if}
            </div>
          {/if}

          <!-- Optimization gate: one progress bar replaces four lines of explanation and a warning box. -->
          <div class="space-y-2 border-t border-border bg-surface/60 px-5 py-4">
            <div class="flex flex-wrap items-baseline justify-between gap-x-4 gap-y-1 text-[13px]">
              <span class="text-foreground" data-testid={`preset-${p.id}-reviews`}>{$t('presets.reviews.available', { count: p.gate.reviews })}</span>
              <span class="text-muted-foreground">{$t('presets.reviews.threshold', { min: p.gate.min })}</span>
            </div>
            <div
              class="h-1.5 overflow-hidden rounded-full bg-muted"
              role="progressbar"
              aria-label={$t('presets.reviews.threshold', { min: p.gate.min })}
              aria-valuemin={0}
              aria-valuemax={p.gate.min}
              aria-valuenow={Math.min(p.gate.reviews, p.gate.min)}
            >
              <div class="h-full rounded-full transition-[width] duration-300 {p.gate.eligible ? 'bg-success' : 'bg-brand'}" style="width: {progress * 100}%"></div>
            </div>
            <p class="text-xs text-muted-foreground">
              {#if !p.gate.eligible}
                <span role="status" data-testid={`preset-${p.id}-shortfall`} class="font-medium text-foreground/80">
                  {$t('presets.optimize.shortfall', { count: p.gate.shortfall })}
                </span>
              {/if}
              {$t('presets.reschedule.note')}
            </p>
          </div>

          {#if p.job}
            <section class="space-y-2 border-t border-border px-5 py-4" data-testid={`preset-${p.id}-job`}>
              <p class="text-sm font-semibold" data-testid={`preset-${p.id}-status`}>{$t(jobStatusKey(p.job.status))}</p>
              {#if p.job.stage}
                <p class="text-[13px] text-muted-foreground" data-testid={`preset-${p.id}-stage`}>{$t(stageKey(p.job.stage))}</p>
              {/if}
              {#if p.job.error}
                <p class="text-[13px] text-destructive-foreground" data-testid={`preset-${p.id}-error`}>{p.job.error}</p>
              {/if}
              {#if p.job.log_tail}
                <div>
                  <p class="text-xs text-muted-foreground">{$t('presets.optimize.log_tail')}</p>
                  <pre class="mt-1.5 max-h-48 overflow-auto rounded-md bg-zinc-950 p-3 font-mono text-[11px] text-zinc-100" data-testid={`preset-${p.id}-log`}>{p.job.log_tail}</pre>
                </div>
              {/if}
              {#if p.job.result && p.job.result.verdict !== 'unavailable'}
                <div class="space-y-2" data-testid={`preset-${p.id}-result`}>
                  <h3 class="text-xs text-muted-foreground">{$t('presets.optimize.result_title')}</h3>
                  <p class="text-[13px] text-muted-foreground">{$t('presets.optimize.result_reviews', { count: p.job.result.reviews_used })}</p>
                  {#if showsFit(p.job.result.verdict)}
                    <div class="grid grid-cols-1 gap-2 font-mono text-xs sm:grid-cols-2">
                      <p class="rounded-md bg-surface p-2" data-testid={`preset-${p.id}-fit-before`}>
                        {fit('presets.optimize.result_before', p.job.result.fit_before.log_loss, p.job.result.fit_before.rmse)}
                      </p>
                      <p class="rounded-md bg-surface p-2" data-testid={`preset-${p.id}-fit-after`}>
                        {fit('presets.optimize.result_after', p.job.result.fit_after.log_loss, p.job.result.fit_after.rmse)}
                      </p>
                    </div>
                    <p class="text-sm font-medium text-success" data-testid={`preset-${p.id}-verdict`}>
                      {$t(verdictKey(p.job.result.verdict))}
                    </p>
                  {:else}
                    <p class="text-sm font-medium text-warning" data-testid={`preset-${p.id}-verdict`}>
                      {$t(verdictKey(p.job.result.verdict))}
                    </p>
                  {/if}
                </div>
              {/if}
            </section>
          {/if}
        </article>
      {/each}
    </div>
  {/if}
</Page>

{#if formOpen}
  <Dialog
    open={true}
    onOpenChange={(open) => { if (!open) closeForm(); }}
    title={formMode === 'create' ? $t('presets.new.heading') : $t('presets.edit.heading')}
    size="lg"
    testId="presets-form-dialog"
  >
      {#if formError}
        <p role="alert" data-testid="presets-form-error" class="text-sm text-destructive-foreground">{$t(formError)}</p>
      {/if}
      <form onsubmit={submitForm} data-testid="presets-form" class="space-y-4">
        <label class="block">
          <span class="text-sm font-medium text-foreground">{$t('presets.form.name')}</span>
          <input
            type="text"
            required
            readonly={formNameLocked}
            bind:value={formName}
            data-testid="presets-form-name"
            class="field-input text-sm mt-1 block w-full"
          />
          {#if formNameLocked}
            <span data-testid="presets-form-name-locked" class="mt-1 block text-xs text-muted-foreground">
              {$t('presets.form.name_locked')}
            </span>
          {/if}
        </label>
        <!-- The help button sits outside the label: a button inside a label becomes its associated control, so clicking the label text would open the popover. -->
        <div>
          <div class="flex items-center gap-1">
            <label for="presets-form-retention" class="text-sm font-medium text-foreground">{$t('presets.form.retention')}</label>
            <HelpTip testId="presets-form-retention-help" label={$t('presets.form.retention')} text={$t('help.presets.retention')} />
          </div>
          <input
            id="presets-form-retention"
            type="text"
            inputmode="decimal"
            required
            bind:value={formRetention}
            data-testid="presets-form-retention"
            class="field-input text-sm mt-1 block w-full"
          />
        </div>
        <div>
          <div class="flex items-center gap-1">
            <label for="presets-form-learning-steps" class="text-sm font-medium text-foreground">{$t('presets.form.learning_steps')}</label>
            <HelpTip testId="presets-form-learning-steps-help" label={$t('presets.form.learning_steps')} text={$t('help.presets.learning_steps')} />
          </div>
          <input
            id="presets-form-learning-steps"
            type="text"
            bind:value={formLearning}
            data-testid="presets-form-learning-steps"
            class="field-input text-sm mt-1 block w-full"
          />
        </div>
        <div>
          <div class="flex items-center gap-1">
            <label for="presets-form-relearning-steps" class="text-sm font-medium text-foreground">{$t('presets.form.relearning_steps')}</label>
            <HelpTip testId="presets-form-relearning-steps-help" label={$t('presets.form.relearning_steps')} text={$t('help.presets.relearning_steps')} />
          </div>
          <input
            id="presets-form-relearning-steps"
            type="text"
            bind:value={formRelearning}
            data-testid="presets-form-relearning-steps"
            class="field-input text-sm mt-1 block w-full"
          />
        </div>
        <div>
          <div class="flex items-center gap-1">
            <label for="presets-form-max-interval" class="text-sm font-medium text-foreground">{$t('presets.form.max_interval')}</label>
            <HelpTip testId="presets-form-max-interval-help" label={$t('presets.form.max_interval')} text={$t('help.presets.max_interval')} />
          </div>
          <!-- No bind:value: binding a number input coerces the value to number, while the state here is a string,
               validated as a "positive integer" by parseForm; only a hand-written value/oninput keeps the raw text. -->
          <input
            id="presets-form-max-interval"
            type="number"
            min="1"
            required
            value={formMaxInterval}
            oninput={(event) => (formMaxInterval = event.currentTarget.value)}
            data-testid="presets-form-max-interval"
            class="field-input text-sm mt-1 block w-full"
          />
        </div>
        <div class="flex items-center gap-2">
          <Checkbox bind:checked={formFuzz} testId="presets-form-fuzz" label={$t('presets.form.fuzz')} />
          <span class="text-sm text-foreground/80">{$t('presets.form.fuzz')}</span>
          <HelpTip testId="presets-form-fuzz-help" label={$t('presets.form.fuzz')} text={$t('help.presets.fuzz')} />
        </div>
        {#if formMode === 'edit'}
          <p class="text-xs text-muted-foreground">{$t('presets.form.edit_note')}</p>
        {/if}
        <div class="flex items-center gap-2 pt-2">
          <Button type="submit" size="lg" testId="presets-form-submit" loading={formSaving}>
            {$t('presets.form.save')}
          </Button>
          <Button variant="outline" size="lg" testId="presets-form-cancel" onclick={closeForm}>
            {$t('presets.form.cancel')}
          </Button>
        </div>
      </form>
  </Dialog>
{/if}

<!-- Second-step delete-preset confirmation dialog (Modal) -->
{#if presetToDelete}
  <Dialog
    open={true}
    onOpenChange={(open) => { if (!open) presetToDelete = null; }}
    title={$t('presets.delete.confirm_title')}
    description={$t('presets.delete.confirm_desc', { name: presetToDelete ? presetDisplayName(presetToDelete.name, presetToDelete.is_default, $t) : '' })}
    testId="preset-delete-dialog"
  >
      {#if deletePresetError}
        <p role="alert" class="text-sm text-destructive-foreground">{$t(deletePresetError)}</p>
      {/if}

      <div class="pt-2 flex items-center justify-end gap-3">
        <Button variant="outline" size="lg" disabled={deletingPreset} onclick={() => presetToDelete = null}>
          {$t('presets.delete.cancel_btn')}
        </Button>
        <Button variant="danger" size="lg" loading={deletingPreset} onclick={confirmDeletePreset} testId="preset-delete-confirm">
          {$t('presets.delete.confirm_btn')}
        </Button>
      </div>
  </Dialog>
{/if}
