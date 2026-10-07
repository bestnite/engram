<script lang="ts">
  import { onMount, onDestroy } from 'svelte';
  import { t } from '../i18n';
  import { apiClient, ApiClientError } from '../api';
  import Dialog from '../components/ui/Dialog.svelte';
  import Button from '../components/ui/Button.svelte';
  import Checkbox from '../components/ui/Checkbox.svelte';
  import Skeleton from '../components/ui/Skeleton.svelte';
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

  // 新建/编辑对话框共用一个表单；formMode 决定提交到 create 还是 update。
  let formOpen = $state(false);
  let formMode = $state<'create' | 'edit'>('create');
  let formTargetId = $state<number | null>(null);
  let formName = $state('');
  let formRetention = $state('0.90');
  let formLearning = $state('1m,10m');
  let formRelearning = $state('10m');
  let formMaxInterval = $state('36500');
  let formFuzz = $state(true);
  let formSaving = $state(false);
  let formError = $state('');

  // notice 是按预设定位的一次性操作提示（i18n key）；gate 的「还差 N 条」单独由门槛渲染。
  let notice = $state<{ id: number; key: string } | null>(null);
  let busyId = $state<number | null>(null);

  // 关联卡组与删除预设状态
  let decks = $state<Deck[]>([]);
  let presetToDelete = $state<PresetRecord | null>(null);
  let deletingPreset = $state(false);
  let deletePresetError = $state('');

  let pollTimer: ReturnType<typeof setInterval> | null = null;
  let pollPresetId: number | null = null;
  let pollJobId: number | null = null;

  const presets = $derived(data ? data.presets : []);
  const gate = $derived(data ? data.gate : null);

  /** 读取预设列表与优化门槛；服务端补齐默认预设。 */
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

  function getPresetDecks(presetId: number): Deck[] {
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

  /** 刷新后有在途作业就续上轮询，避免刷新丢掉正在跑的任务状态。 */
  function resumePolling(): void {
    if (!data) return;
    const active = data.presets.find((p) => p.job && isActiveJob(p.job));
    if (active && active.job) {
      startPolling(active.id, active.job.id);
    }
  }

  /** 只接受非负整数用于最大间隔；正则先挡下小数与非数字。 */
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

  /** 解析表单并做与后端一致的校验；返回 i18n key 字符串表示拒绝。 */
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

  /** 触发优化：成功起轮询；不足门槛/已有作业刷新门槛并给一次提示。 */
  async function runOptimize(p: PresetRecord): Promise<void> {
    notice = null;
    busyId = p.id;
    try {
      const resp = await apiClient.optimizePreset(p.id);
      applyJob(p.id, resp.job, resp.gate);
      if (resp.job && isActiveJob(resp.job)) {
        startPolling(p.id, resp.job.id);
      }
    } catch (err) {
      if (err instanceof ApiClientError && err.code === 'insufficient_reviews') {
        // 不足门槛：门槛段落会随刷新渲染「还差 N 条」，无需额外提示。
        await load();
      } else if (err instanceof ApiClientError && err.code === 'optimize_conflict') {
        await load();
        notice = { id: p.id, key: 'presets.optimize.conflict' };
      } else {
        notice = { id: p.id, key: 'presets.error.failed' };
      }
    } finally {
      busyId = null;
    }
  }

  /** 一键回退默认权重；响应即整份列表，权重来源随之回到默认。 */
  async function revertWeights(p: PresetRecord): Promise<void> {
    notice = null;
    busyId = p.id;
    try {
      data = await apiClient.revertPresetWeights(p.id);
    } catch (err) {
      notice = { id: p.id, key: optimizationErrorKey(err) };
    } finally {
      busyId = null;
    }
  }

  function startPolling(presetId: number, jobId: number): void {
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
        // 完成后就地刷新整份列表：权重来源与结果摘要永远来自同一次数据库读取。
        await load();
      }
    } catch {
      // 轮询失败不静默：给出一次错误提示并停止，用户可手动重试。
      notice = { id: presetId, key: 'presets.error.failed' };
      stopPolling();
    }
  }

  /** 就地更新某个预设的作业与门槛，不改动其它预设。 */
  function applyJob(presetId: number, job: PresetJob | null, nextGate: OptimizeGate): void {
    if (!data) return;
    data = {
      gate: nextGate,
      presets: data.presets.map((p) => (p.id === presetId ? { ...p, job } : p)),
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

  /** 只有两侧指标样本都足够时才渲染 before/after 与结论（M9-12）。 */
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

<div class="mx-auto max-w-4xl space-y-6 px-4 py-10" data-testid="presets-view">
  <div class="flex flex-col sm:flex-row sm:items-center sm:justify-between gap-4">
    <h1 class="text-2xl font-bold tracking-tight text-zinc-900 dark:text-zinc-100" data-testid="presets-heading">
      {$t('presets.heading')}
    </h1>
    <button
      type="button"
      data-testid="presets-new"
      onclick={openCreate}
      class="inline-flex items-center rounded-xl bg-zinc-900 px-4 py-2 text-sm font-semibold text-white hover:bg-zinc-800 dark:bg-zinc-100 dark:text-zinc-900 dark:hover:bg-white transition-colors cursor-pointer"
    >
      {$t('presets.new.button')}
    </button>
  </div>

  {#if loading}
    <Skeleton testId="presets-loading" label={$t('common.loading')} />
  {:else if loadError}
    <div data-testid="presets-failed" class="card-elevated p-8 rounded-xl text-center">
      <p role="alert" class="text-base font-medium text-zinc-900 dark:text-zinc-100">{$t(loadErrorKey())}</p>
      <button
        type="button"
        data-testid="presets-retry"
        onclick={load}
        class="inline-flex items-center gap-2 mt-4 px-4 py-2 text-sm font-medium rounded-xl bg-zinc-900 text-white dark:bg-zinc-100 dark:text-zinc-900 hover:bg-zinc-800 dark:hover:bg-zinc-200 transition-colors btn-press cursor-pointer"
      >
        <svg class="w-4 h-4" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round">
          <path d="M3 12a9 9 0 0 1 15-6.7L21 8" /><path d="M21 3v5h-5" /><path d="M21 12a9 9 0 0 1-15 6.7L3 16" /><path d="M3 21v-5h5" />
        </svg>
        <span>{$t('common.retry')}</span>
      </button>
    </div>
  {:else if data && presets.length === 0}
    <div data-testid="presets-empty" class="card-elevated p-12 rounded-xl text-center">
      <p class="text-sm font-medium text-zinc-500 dark:text-zinc-400">{$t('presets.empty')}</p>
    </div>
  {:else if data}
    <div class="space-y-6" data-testid="presets-list">
      {#each presets as p (p.id)}
        <article data-testid={`preset-${p.id}`} class="card-elevated rounded-xl p-6 space-y-5">
          <div class="flex flex-col sm:flex-row sm:items-center sm:justify-between gap-3 border-b border-zinc-100 dark:border-zinc-800 pb-4">
            <div>
              <h2 class="text-lg font-bold tracking-tight text-zinc-900 dark:text-zinc-100" data-testid={`preset-${p.id}-name`}>
                {p.name}
              </h2>
              <div class="flex items-center gap-2 text-xs pt-1.5">
                <span class="text-zinc-500 dark:text-zinc-400">{$t('presets.used_by_decks')}</span>
                {#if getPresetDecks(p.id).length > 0}
                  <div class="flex flex-wrap gap-1.5">
                    {#each getPresetDecks(p.id) as d (d.id)}
                      <a href={`/decks/${d.id}`} class="px-2 py-0.5 rounded-md bg-blue-50 dark:bg-blue-950/40 text-blue-700 dark:text-blue-300 border border-blue-200/60 dark:border-blue-800/60 font-medium hover:underline">
                        {d.name}
                      </a>
                    {/each}
                  </div>
                {:else}
                  <span class="text-zinc-400 dark:text-zinc-500 font-normal">{$t('presets.unused')}</span>
                {/if}
              </div>
            </div>
            <div class="flex flex-wrap items-center gap-2">
              <button
                type="button"
                data-testid={`preset-${p.id}-edit`}
                onclick={() => openEdit(p)}
                class="px-3 py-1.5 text-xs font-semibold rounded-lg border border-zinc-200 dark:border-zinc-700 hover:bg-zinc-50 dark:hover:bg-zinc-800 transition-colors cursor-pointer"
              >
                {$t('presets.edit.button')}
              </button>
              <button
                type="button"
                data-testid={`preset-${p.id}-optimize`}
                disabled={busyId === p.id || (gate !== null && !gate.eligible)}
                title={gate && !gate.eligible ? $t('presets.optimize.gate_shortfall', { count: gate.shortfall }) : ''}
                onclick={() => runOptimize(p)}
                class="px-3.5 py-1.5 text-xs font-semibold rounded-lg bg-zinc-900 text-white dark:bg-zinc-100 dark:text-zinc-900 hover:bg-zinc-800 dark:hover:bg-white disabled:opacity-50 disabled:cursor-not-allowed transition-colors cursor-pointer"
              >
                {$t('presets.optimize.button')}
              </button>
              <button
                type="button"
                data-testid={`preset-${p.id}-revert`}
                disabled={busyId === p.id}
                onclick={() => revertWeights(p)}
                class="px-3.5 py-1.5 text-xs font-semibold rounded-lg border border-zinc-200 dark:border-zinc-700 hover:bg-zinc-50 dark:hover:bg-zinc-800 disabled:opacity-60 transition-colors cursor-pointer"
              >
                {$t('presets.revert.button')}
              </button>
              {#if p.name !== 'Default'}
                <button
                  type="button"
                  data-testid={`preset-${p.id}-delete`}
                  disabled={busyId === p.id || getPresetDecks(p.id).length > 0}
                  title={getPresetDecks(p.id).length > 0 ? $t('presets.delete.in_use') : $t('presets.delete.action')}
                  onclick={() => promptDeletePreset(p)}
                  class="px-3 py-1.5 text-xs font-semibold rounded-lg border border-rose-200 dark:border-rose-900 text-rose-700 dark:text-rose-400 hover:bg-rose-50 dark:hover:bg-rose-950/40 disabled:opacity-40 disabled:cursor-not-allowed transition-colors cursor-pointer"
                >
                  {$t('presets.delete.action')}
                </button>
              {/if}
            </div>
          </div>

          <dl class="grid grid-cols-1 gap-4 sm:grid-cols-3">
            <div class="rounded-xl border border-zinc-100 dark:border-zinc-800 p-4">
              <dt class="text-xs font-semibold uppercase tracking-wider text-zinc-500 dark:text-zinc-400">{$t('presets.retention_label')}</dt>
              <dd class="mt-1 text-base font-bold font-mono" data-testid={`preset-${p.id}-retention`}>{p.desired_retention.toFixed(2)}</dd>
            </div>
            <div class="rounded-xl border border-zinc-100 dark:border-zinc-800 p-4">
              <dt class="text-xs font-semibold uppercase tracking-wider text-zinc-500 dark:text-zinc-400">{$t('presets.max_interval_label')}</dt>
              <dd class="mt-1 text-base font-bold font-mono" data-testid={`preset-${p.id}-max-interval`}>{p.maximum_interval_days}</dd>
            </div>
            <div class="rounded-xl border border-zinc-100 dark:border-zinc-800 p-4">
              <dt class="text-xs font-semibold uppercase tracking-wider text-zinc-500 dark:text-zinc-400">{$t('presets.fuzz_label')}</dt>
              <dd class="mt-1 text-base font-bold font-mono" data-testid={`preset-${p.id}-fuzz`}>{$t(fuzzValueKey(p))}</dd>
            </div>
          </dl>

          <section class="rounded-xl border border-zinc-200/80 dark:border-zinc-800 p-5 space-y-2">
            <div class="flex items-center justify-between">
              <h3 class="text-xs font-semibold uppercase tracking-wider text-zinc-500 dark:text-zinc-400">{$t('presets.weights.heading')}</h3>
              <span class="rounded-md px-2 py-0.5 text-xs font-medium" data-testid={`preset-${p.id}-weights-source`}>
                {$t(weightsSourceKey(p))}
              </span>
            </div>
            {#if p.weights_optimized_at}
              <p class="text-xs text-zinc-500 dark:text-zinc-400">{$t('presets.weights.optimized_at', { time: p.weights_optimized_at })}</p>
            {/if}
            {#if p.weights_review_count !== null}
              <p class="text-xs text-zinc-500 dark:text-zinc-400">{$t('presets.weights.review_count', { count: p.weights_review_count })}</p>
            {/if}
            {#if p.weights_raw}
              <details>
                <summary class="cursor-pointer text-xs font-medium text-indigo-600 dark:text-indigo-400">{$t('presets.weights.raw_label')}</summary>
                <code class="mt-2 block max-h-32 overflow-auto rounded-lg border border-zinc-200 dark:border-zinc-700 p-3 font-mono text-[11px] break-all" data-testid={`preset-${p.id}-weights-raw`}>{p.weights_raw}</code>
              </details>
            {/if}
          </section>

          {#if gate}
            <div class="space-y-1 text-xs text-zinc-500 dark:text-zinc-400">
              <p data-testid={`preset-${p.id}-reviews`}>{$t('presets.reviews.available', { count: gate.reviews })}</p>
              <p>{$t('presets.reviews.threshold', { min: gate.min })}</p>
              <p>{$t('presets.reschedule.note')}</p>
              <p>{$t('presets.revert.note')}</p>
            </div>
            {#if !gate.eligible}
              <p role="status" data-testid={`preset-${p.id}-shortfall`} class="rounded-xl border border-amber-200 dark:border-amber-900/60 px-4 py-3 text-xs text-amber-800 dark:text-amber-300">
                {$t('presets.optimize.shortfall', { count: gate.shortfall, have: gate.reviews, min: gate.min })}
              </p>
            {/if}
          {/if}

          {#if notice && notice.id === p.id}
            <p role="alert" data-testid={`preset-${p.id}-notice`} class="rounded-xl border border-amber-200 dark:border-amber-900/60 px-4 py-3 text-xs text-amber-800 dark:text-amber-300">
              {$t(notice.key)}
            </p>
          {/if}

          {#if p.job}
            <section class="rounded-xl border border-zinc-200/80 dark:border-zinc-800 p-4 space-y-2" data-testid={`preset-${p.id}-job`}>
              <p class="text-sm font-semibold" data-testid={`preset-${p.id}-status`}>{$t(jobStatusKey(p.job.status))}</p>
              {#if p.job.stage}
                <p class="text-xs text-zinc-500 dark:text-zinc-400" data-testid={`preset-${p.id}-stage`}>{$t(stageKey(p.job.stage))}</p>
              {/if}
              {#if p.job.error}
                <p class="rounded-lg border border-rose-200 dark:border-rose-900/60 px-3 py-2 text-xs text-rose-700 dark:text-rose-300" data-testid={`preset-${p.id}-error`}>{p.job.error}</p>
              {/if}
              {#if p.job.log_tail}
                <div>
                  <p class="text-xs font-semibold uppercase tracking-wider text-zinc-400 dark:text-zinc-500">{$t('presets.optimize.log_tail')}</p>
                  <pre class="mt-1.5 max-h-48 overflow-auto rounded-lg bg-zinc-950 p-3 font-mono text-[11px] text-zinc-100" data-testid={`preset-${p.id}-log`}>{p.job.log_tail}</pre>
                </div>
              {/if}
              {#if p.job.result && p.job.result.verdict !== 'unavailable'}
                <div class="space-y-2" data-testid={`preset-${p.id}-result`}>
                  <h4 class="text-xs font-semibold uppercase tracking-wider text-zinc-500 dark:text-zinc-400">{$t('presets.optimize.result_title')}</h4>
                  <p class="text-xs text-zinc-500 dark:text-zinc-400">{$t('presets.optimize.result_reviews', { count: p.job.result.reviews_used })}</p>
                  {#if showsFit(p.job.result.verdict)}
                    <div class="grid grid-cols-1 sm:grid-cols-2 gap-2 text-xs font-mono">
                      <p class="rounded-lg bg-zinc-50 dark:bg-zinc-800 p-2" data-testid={`preset-${p.id}-fit-before`}>
                        {fit('presets.optimize.result_before', p.job.result.fit_before.log_loss, p.job.result.fit_before.rmse)}
                      </p>
                      <p class="rounded-lg bg-zinc-50 dark:bg-zinc-800 p-2" data-testid={`preset-${p.id}-fit-after`}>
                        {fit('presets.optimize.result_after', p.job.result.fit_after.log_loss, p.job.result.fit_after.rmse)}
                      </p>
                    </div>
                    <p class="text-sm font-semibold text-emerald-700 dark:text-emerald-400" data-testid={`preset-${p.id}-verdict`}>
                      {$t(verdictKey(p.job.result.verdict))}
                    </p>
                  {:else}
                    <p class="text-sm font-medium text-amber-700 dark:text-amber-400" data-testid={`preset-${p.id}-verdict`}>
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
</div>

{#if formOpen}
  <Dialog
    open={true}
    onOpenChange={(open) => { if (!open) closeForm(); }}
    title={formMode === 'create' ? $t('presets.new.heading') : $t('presets.edit.heading')}
    size="lg"
    testId="presets-form-dialog"
  >
      {#if formError}
        <p role="alert" data-testid="presets-form-error" class="rounded-xl border border-rose-200 dark:border-rose-900/60 px-4 py-3 text-sm text-rose-700 dark:text-rose-300">{formError}</p>
      {/if}
      <form onsubmit={submitForm} data-testid="presets-form" class="space-y-4">
        <label class="block">
          <span class="text-xs font-semibold uppercase tracking-wider text-zinc-500 dark:text-zinc-400">{$t('presets.form.name')}</span>
          <input
            type="text"
            required
            bind:value={formName}
            data-testid="presets-form-name"
            class="field-input text-sm mt-1 block w-full"
          />
        </label>
        <label class="block">
          <span class="text-xs font-semibold uppercase tracking-wider text-zinc-500 dark:text-zinc-400">{$t('presets.form.retention')}</span>
          <input
            type="text"
            inputmode="decimal"
            required
            bind:value={formRetention}
            data-testid="presets-form-retention"
            class="field-input text-sm mt-1 block w-full"
          />
        </label>
        <label class="block">
          <span class="text-xs font-semibold uppercase tracking-wider text-zinc-500 dark:text-zinc-400">{$t('presets.form.learning_steps')}</span>
          <input
            type="text"
            bind:value={formLearning}
            data-testid="presets-form-learning-steps"
            class="field-input text-sm mt-1 block w-full"
          />
        </label>
        <label class="block">
          <span class="text-xs font-semibold uppercase tracking-wider text-zinc-500 dark:text-zinc-400">{$t('presets.form.relearning_steps')}</span>
          <input
            type="text"
            bind:value={formRelearning}
            data-testid="presets-form-relearning-steps"
            class="field-input text-sm mt-1 block w-full"
          />
        </label>
        <label class="block">
          <span class="text-xs font-semibold uppercase tracking-wider text-zinc-500 dark:text-zinc-400">{$t('presets.form.max_interval')}</span>
          <input
            type="number"
            min="1"
            required
            bind:value={formMaxInterval}
            data-testid="presets-form-max-interval"
            class="field-input text-sm mt-1 block w-full"
          />
        </label>
        <div class="flex items-center gap-2">
          <Checkbox bind:checked={formFuzz} testId="presets-form-fuzz" label={$t('presets.form.fuzz')} />
          <span class="text-sm text-zinc-700 dark:text-zinc-300">{$t('presets.form.fuzz')}</span>
        </div>
        {#if formMode === 'edit'}
          <p class="text-xs text-zinc-500 dark:text-zinc-400">{$t('presets.form.edit_note')}</p>
        {/if}
        <div class="flex items-center gap-2 pt-2">
          <Button type="submit" size="lg" testId="presets-form-submit" disabled={formSaving}>
            {formSaving ? $t('presets.form.saving') : $t('presets.form.save')}
          </Button>
          <Button variant="outline" size="lg" testId="presets-form-cancel" onclick={closeForm}>
            {$t('presets.form.cancel')}
          </Button>
        </div>
      </form>
  </Dialog>
{/if}

<!-- 删除预设二次确认对话框（Modal） -->
{#if presetToDelete}
  <Dialog
    open={true}
    onOpenChange={(open) => { if (!open) presetToDelete = null; }}
    title={$t('presets.delete.confirm_title')}
    description={$t('presets.delete.confirm_desc', { name: presetToDelete.name })}
    testId="preset-delete-dialog"
  >
      {#if deletePresetError}
        <p role="alert" class="text-xs text-rose-600 dark:text-rose-400">{$t(deletePresetError)}</p>
      {/if}

      <div class="pt-2 flex items-center justify-end gap-3">
        <Button variant="outline" size="lg" disabled={deletingPreset} onclick={() => presetToDelete = null}>
          {$t('presets.delete.cancel_btn')}
        </Button>
        <Button variant="danger" size="lg" disabled={deletingPreset} onclick={confirmDeletePreset} testId="preset-delete-confirm">
          {deletingPreset ? $t('common.deleting') : $t('presets.delete.confirm_btn')}
        </Button>
      </div>
  </Dialog>
{/if}
