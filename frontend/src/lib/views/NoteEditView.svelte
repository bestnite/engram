<script lang="ts">
  import { onMount, tick, untrack } from 'svelte';
  import { routeStore, navigate } from '../router';
  import { t } from '../i18n';
  import { apiClient, ApiClientError } from '../api';
  import type { Note } from '../api';
  import { typeset } from '../mathjax';
  import MediaPicker from '../components/MediaPicker.svelte';
  import NoteFieldsForm from '../components/NoteFieldsForm.svelte';
  import Select from '../components/ui/Select.svelte';
  import Button from '../components/ui/Button.svelte';
  import Panel from '../components/ui/Panel.svelte';
  import { emptyFields, toFormFields, toPayloadFields } from '../card-fields';
  import { cardTypes, fieldsForKind, kindOrder, loadCardTypes } from '../card-types';

  // 一个实现同时承担新建与编辑：地址上有没有 noteId 决定模式，界面其余部分逐项相同
  // （题型、字段、标签、媒体与自动预览）。新建成功固定跳回卡组页，不留第二条「再建一条」路径。
  const deckId = $derived($routeStore.params.id || '');
  const noteId = $derived($routeStore.params.noteId || '');
  const isCreate = $derived(noteId === '');

  // 自动预览的防抖窗口：字段停手约半秒后再请求，避免逐字符打请求。
  const PREVIEW_DEBOUNCE_MS = 500;

  let note = $state<Note | null>(null);
  let kind = $state('basic');
  let fields = $state<Record<string, unknown>>({});
  // 该题型字段表之外的键（外部写入的自定义键）原样保留，保存时合回，绝不静默丢弃。
  let unknownKeys = $state<Record<string, unknown>>({});
  let tagsText = $state('');

  let loading = $state(true);
  let saving = $state(false);
  let loadError = $state(false);
  let saveError = $state(false);
  let invalid = $state(false);
  let saved = $state(false);
  let metaError = $state(false);

  let selectedField = $state('');
  // 上传控件状态：与媒体库选择器共用 insertMedia 的插入路径（编辑器媒体面）。
  let uploadInput = $state<HTMLInputElement | null>(null);
  let uploading = $state(false);
  let uploadErrorKey = $state('');
  let uploaded = $state(false);

  // 预览状态机：empty 尚未输入 / loading 生成中（可能仍挂着上一份结果）/ ready 有结果 /
  // incomplete 字段不全（服务端 400）/ error 取用失败。
  type PreviewStatus = 'empty' | 'loading' | 'ready' | 'incomplete' | 'error';
  let previewStatus = $state<PreviewStatus>('empty');
  let previewCards = $state<Array<{ front_html: string; back_html: string }> | null>(null);
  // 只对它调用 MathJax 排版：它是唯一承载服务端清洗后 HTML 的容器。
  let previewSection = $state<HTMLElement | null>(null);
  let previewReady = $state(false);

  // 非响应式控制位：卸载后不得再写状态；generation 保证只有最新一次请求能落地。
  let disposed = false;
  let previewGeneration = 0;
  let debounceTimer: ReturnType<typeof setTimeout> | null = null;
  let typesetChain: Promise<void> = Promise.resolve();
  // 最近一次成功保存时的草稿签名；草稿偏离它即表示有未保存改动。
  let savedSignature = '';

  // 题型下拉：清单与顺序来自服务端的自描述（kind 字典序），元数据未就绪时为空。
  const kindOptions = $derived(
    kindOrder($cardTypes).map((value) => ({ value, label: $t(`notes.kind.${value}` as never) }))
  );

  // 可插入媒体的字段：只有文本类字段能承载 Markdown 图片引用。
  const textFields = $derived(
    fieldsForKind($cardTypes, kind)
      .filter((spec) => spec.control === 'text' || spec.control === 'textarea' || spec.control === 'lines')
      .map((spec) => ({ value: spec.key, label: $t('note.fields.' + spec.key) }))
  );

  // 字段内容的稳定签名：kind 或任一字段变化都会得到新串，自动预览据此重排。
  const signature = $derived(JSON.stringify({ kind, fields: collectFields() }));

  // 草稿签名：kind、提交字段与标签任一变化都得到新串（标签用解析后的数组，避免只改空格
  // 也当成新改动）。保存只对提交那一刻的快照负责，因此用它判断响应落地时草稿是否已被改过，
  // 以及何时撤下「已保存」标记。
  const draftSignature = $derived(
    JSON.stringify({ kind, fields: collectFields(), tags: parseTags() })
  );

  function firstTextField(nextKind: string): string {
    const spec = fieldsForKind($cardTypes, nextKind).find(
      (item) => item.control === 'text' || item.control === 'textarea' || item.control === 'lines'
    );
    return spec ? spec.key : '';
  }

  function parseTags(): string[] {
    return tagsText.split(',').map((tag) => tag.trim()).filter(Boolean);
  }

  /** 表单模型 → 提交用字段：认识的键按表清洗，未知键原样带回。 */
  function collectFields(): Record<string, unknown> {
    return { ...unknownKeys, ...toPayloadFields(fields) };
  }

  /**
   * 是否已有可预览内容：只看该题型字段表里的文本 / 数组 / 数值字段，判断它们是否偏离空表单
   * 基线。布尔字段没有可渲染正文，且它的默认值（如 typed 的 ignore_case=true、list 的
   * ordered=false、true_false 的 answer=false）来自题型定义而非用户输入，一律不计；题型表
   * 之外的键（unknownKeys）也不是本次输入，同样不计。数值 0 是真实输入，必须计入。
   */
  function hasContent(): boolean {
    const specs = fieldsForKind($cardTypes, kind);
    const baseline = toPayloadFields(emptyFields(specs));
    const current = toPayloadFields(fields);
    for (const spec of specs) {
      if (spec.control === 'bool') continue;
      if (!(spec.key in current)) continue;
      if (JSON.stringify(current[spec.key]) !== JSON.stringify(baseline[spec.key])) return true;
    }
    return false;
  }

  /** 载入卡片并把服务端字段收进表单模型；返回未知键另存，供保存时合并。 */
  function adoptFields(loaded: Note): void {
    kind = loaded.kind;
    fields = toFormFields(fieldsForKind($cardTypes, loaded.kind), loaded.fields);
    const known = new Set(fieldsForKind($cardTypes, loaded.kind).map((spec) => spec.key));
    unknownKeys = Object.fromEntries(Object.entries(loaded.fields).filter(([key]) => !known.has(key)));
    tagsText = loaded.tags.join(', ');
    selectedField = firstTextField(loaded.kind);
  }

  async function load(): Promise<void> {
    loading = true;
    loadError = false;
    // 先确保题型元数据就绪：字段表随元数据一起到达，未知题型无法初始化表单。
    let catalogReady = true;
    try {
      await loadCardTypes(apiClient);
    } catch {
      if (disposed) return;
      metaError = true;
      catalogReady = false;
    }
    // 卸载/换路由后不再初始化表单：晚到的元数据不得再写状态或触发首屏预览。
    if (disposed) return;
    try {
      if (isCreate) {
        // 新建不读远端卡片，绝不带空 id GET。默认 basic，清单缺该题型时退回第一项。
        const kinds = kindOrder($cardTypes);
        const initialKind = kinds.includes('basic') ? 'basic' : kinds[0] ?? 'basic';
        kind = initialKind;
        fields = emptyFields(fieldsForKind($cardTypes, initialKind));
        unknownKeys = {};
        selectedField = firstTextField(initialKind);
        previewReady = catalogReady;
      } else {
        // 按对外 id 直接读目标卡片；不属于地址上的卡组按「不存在」处理，禁止跨卡组编辑。
        const loaded = await apiClient.getNote(noteId);
        if (disposed) return;
        const loadedNote = loaded && String(loaded.deck_id) === deckId ? loaded : null;
        note = loadedNote;
        if (loadedNote) {
          adoptFields(loadedNote);
          previewReady = catalogReady;
        } else {
          loadError = true;
        }
      }
    } catch {
      if (disposed) return;
      loadError = true;
    } finally {
      if (!disposed) loading = false;
    }
  }

  /**
   * 切换题型：按新题型重新初始化字段（旧字段名不通用，保留只会造成半张卡）。
   * 未知键不动：它们是原卡片上不属任何字段表的数据，换题型也不该顺手丢掉。
   */
  function changeKind(next: string): void {
    kind = next;
    fields = emptyFields(fieldsForKind($cardTypes, next));
    selectedField = firstTextField(next);
    saved = false;
    invalid = false;
    saveError = false;
  }

  // insertMedia 把 Markdown 图片引用追加到当前选中字段；上传与选择器共用同一段逻辑。
  // 只做字符串拼接，绝不把 url 当 HTML 渲染；返回是否真的插入了（字段非法时不动内容）。
  function insertMedia(url: string): boolean {
    const current = fields[selectedField];
    if (typeof current !== 'string') return false;
    fields[selectedField] = `${current}${current && !current.endsWith('\n') ? '\n' : ''}![](${url})`;
    saved = false;
    return true;
  }

  // uploadErrorKeyFor 把服务端稳定错误 code 映射到本地化 key；未知 code 回落到通用失败提示。
  function uploadErrorKeyFor(err: unknown): string {
    const code = err instanceof ApiClientError ? err.code : '';
    const map: Record<string, string> = {
      media_too_large: 'media.upload.too_large',
      media_mime_not_allowed: 'media.upload.mime_not_allowed',
      media_magic_mismatch: 'media.upload.magic_mismatch',
      media_quota_exceeded: 'media.upload.quota_exceeded',
      media_missing_file: 'media.upload.file_required',
    };
    return map[code] || 'media.upload.failed';
  }

  async function uploadMedia(): Promise<void> {
    uploaded = false;
    uploadErrorKey = '';
    const input = uploadInput;
    const file = input?.files?.[0];
    if (!file) {
      uploadErrorKey = 'media.upload.file_required';
      return;
    }
    uploading = true;
    try {
      const result = await apiClient.uploadDeckMedia(deckId, file);
      if (disposed) return;
      uploaded = insertMedia(result.url);
      if (input) input.value = '';
    } catch (err) {
      if (disposed) return;
      uploadErrorKey = uploadErrorKeyFor(err);
    } finally {
      if (!disposed) uploading = false;
    }
  }

  /**
   * 预览调度：清掉待发的一次，推进 generation，再按防抖窗口排新的一次。
   * 有上一份结果时保留它、只把状态标成生成中——预览重排期间绝不闪成空白。
   */
  function schedulePreview(): void {
    if (debounceTimer !== null) {
      clearTimeout(debounceTimer);
      debounceTimer = null;
    }
    const generation = ++previewGeneration;
    if (!hasContent()) {
      previewStatus = 'empty';
      previewCards = null;
      return;
    }
    previewStatus = 'loading';
    debounceTimer = setTimeout(() => {
      debounceTimer = null;
      void runPreview(generation);
    }, PREVIEW_DEBOUNCE_MS);
  }

  /** 一次预览请求的落地结果：成功带卡片，失败带状态（400=字段不全，其余=取用失败）。 */
  type PreviewOutcome =
    | { ok: true; cards: Array<{ front_html: string; back_html: string }> }
    | { ok: false; status: 'incomplete' | 'error' };

  /**
   * 把「写入新卡片 → 等 Svelte 挂上 DOM → MathJax 排版」三步整体挂到串行链尾。
   *
   * 只串行排版不够：新响应若在上一轮排版进行中替换容器内容，上一轮的 MathJax 就会改写已经
   * 换过的 DOM（旧公式插进新卡片）。三步一起串行后，每一轮排版看到的一定是自己那一代提交的
   * 容器，且最新一代最后落地。链中每一步都确认自己仍是最新一代；最新一次失败时不排版。
   */
  function commitPreview(generation: number, outcome: PreviewOutcome): void {
    typesetChain = typesetChain.then(async () => {
      if (disposed || generation !== previewGeneration) return;
      if (!outcome.ok) {
        previewCards = null;
        previewStatus = outcome.status;
        return;
      }
      previewCards = outcome.cards;
      previewStatus = 'ready';
      await tick();
      if (disposed || generation !== previewGeneration) return;
      const container = previewSection;
      if (container) await typeset([container]);
    });
  }

  /** 取一次预览。落地前先确认自己仍是最新一代，过期结果与过期错误都丢弃。 */
  async function runPreview(generation: number): Promise<void> {
    if (disposed || generation !== previewGeneration) return;
    const requestKind = kind;
    const requestFields = collectFields();
    let outcome: PreviewOutcome;
    try {
      const response = await apiClient.previewNote(deckId, requestKind, requestFields);
      if (disposed || generation !== previewGeneration) return;
      outcome = { ok: true, cards: response.cards };
    } catch (err) {
      if (disposed || generation !== previewGeneration) return;
      // 400 是「字段还不全 / 题型无法渲染」，与取用失败分开表达。
      outcome = { ok: false, status: err instanceof ApiClientError && err.status === 400 ? 'incomplete' : 'error' };
    }
    commitPreview(generation, outcome);
  }

  /** 提交入口：新建与编辑共用一条路径，只在落地动作上分叉；在途时直接忽略重复提交。 */
  async function submit(event: SubmitEvent): Promise<void> {
    event.preventDefault();
    if (saving) return;
    invalid = false;
    saved = false;
    saveError = false;
    if (isCreate) await create();
    else await save();
  }

  /** 编辑保存：原地更新，保持卡片身份与标签状态，不跳转。 */
  async function save(): Promise<void> {
    if (!note) return;
    // 提交瞬间冻结草稿：响应只对这一份快照负责。
    const submitted = draftSignature;
    saving = true;
    try {
      const updated = await apiClient.updateNote(note.id, {
        kind,
        fields: collectFields(),
        tags: parseTags(),
      });
      if (disposed) return;
      // 身份先落地：即便草稿在请求期间被改过，后续保存仍指向同一张卡片。
      note = updated;
      if (draftSignature === submitted) {
        // 请求期间没有新输入：可以安全地把响应收进表单并标记已保存。
        adoptFields(updated);
        savedSignature = draftSignature;
        saved = true;
      }
      // 草稿已被改过：保留用户正在输入的内容（不 adopt），也不谎报「已保存」。
    } catch (err) {
      if (disposed) return;
      saveError = true;
      if (err instanceof ApiClientError && err.status === 400) invalid = true;
    } finally {
      if (!disposed) saving = false;
    }
  }

  /** 新建：只有确认新建成功（非 dry_run、created==1、无逐行错误）才跳回卡组页。 */
  async function create(): Promise<void> {
    saving = true;
    try {
      const result = await apiClient.createNotes(deckId, {
        notes: [{ kind, fields: collectFields(), tags: parseTags() }],
      });
      // 用户在请求在途时离开（换路由/卸载）：不再写状态，也不要把他们拉去卡组页。
      if (disposed) return;
      if (result.dry_run || result.created !== 1 || result.errors.length > 0) {
        saveError = true;
        return;
      }
      navigate(`/decks/${encodeURIComponent(deckId)}`);
    } catch (err) {
      if (disposed) return;
      saveError = true;
      if (err instanceof ApiClientError && err.status === 400) invalid = true;
    } finally {
      if (!disposed) saving = false;
    }
  }

  // 自动预览的触发点：装载完成（首屏预览）之后，kind 或字段内容每次变化都重排一次。
  // 调度用 untrack 包住：调度里会读 previewCards/previewStatus，若被记成依赖，就会形成
  // 「预览完成 → 又排一次预览」的自激循环；真正的依赖只有 previewReady 与 signature。
  $effect(() => {
    if (!previewReady) return;
    const current = signature;
    void current;
    untrack(() => schedulePreview());
  });

  // 任何改动（含标签）都让「已保存」失效：保存成功只代表提交那一刻的草稿快照。
  $effect(() => {
    if (draftSignature === savedSignature) return;
    saved = false;
  });

  onMount(() => {
    void load();
    return () => {
      // 卸载/换路由：停掉待发的防抖，并让所有在途回调在写状态前退出。
      disposed = true;
      if (debounceTimer !== null) {
        clearTimeout(debounceTimer);
        debounceTimer = null;
      }
    };
  });
</script>

<section class="py-10 max-w-6xl mx-auto px-4">
  <a href="/decks/{encodeURIComponent(deckId)}" title={$t('note_edit.back')} class="inline-flex items-center gap-1.5 text-sm text-zinc-500 hover:text-zinc-900 dark:text-zinc-400 dark:hover:text-zinc-100 transition-colors">
    <svg class="w-4 h-4" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round" aria-hidden="true"><polyline points="15 18 9 12 15 6"/></svg>
    <span>{$t('note_edit.back')}</span>
  </a>
  <h1 class="text-2xl font-bold my-5">
    {isCreate ? $t('note_create.title') : $t('note_edit.title', { id: noteId })}
  </h1>
  {#if loading || (note && !$cardTypes && !metaError)}
    <p class="text-sm text-zinc-500 dark:text-zinc-400">{$t('note_edit.loading')}</p>
  {:else if !isCreate && loadError && !note}
    <p role="alert" data-testid="note-edit-not-found" class="text-sm text-rose-600 dark:text-rose-400">{$t('note_edit.not_found')}</p>
  {:else if metaError && !$cardTypes}
    <p role="alert" data-testid="note-edit-metadata-error" class="text-sm text-rose-600 dark:text-rose-400">{$t('common.error')}</p>
  {:else}
    <form onsubmit={submit} data-testid="note-editor-form">
      <div class="grid gap-5 lg:grid-cols-2 lg:items-start">
        <!-- 左栏：输入。题型、字段、标签与媒体面。 -->
        <div class="space-y-5">
          <Panel class="space-y-5">
            <div>
              <span class="block text-xs font-semibold uppercase tracking-wider text-zinc-500 dark:text-zinc-400">
                {$t('note_edit.kind')}
              </span>
              <Select
                class="mt-1.5 w-full"
                testId="note-edit-kind"
                value={kind}
                onValueChange={changeKind}
                options={kindOptions}
              />
              <p class="mt-1 text-xs text-zinc-400 dark:text-zinc-500">{$t('note_edit.kind_hint')}</p>
            </div>

            <NoteFieldsForm specs={fieldsForKind($cardTypes, kind)} {fields} testIdPrefix="note-field" />

            <label class="block text-sm font-medium text-zinc-700 dark:text-zinc-300">{$t('note_edit.tags')}
              <input data-testid="note-tags-editor" bind:value={tagsText} class="field-input text-sm mt-1.5 w-full" />
            </label>
          </Panel>

          <!-- 选择器只引用原图；延迟加载与限高由 CSS 负责。 -->
          <Panel padding="md" class="space-y-3">
            <label class="block text-sm font-medium text-zinc-700 dark:text-zinc-300">{$t('media.field')}
              <Select
                class="mt-1.5 w-full"
                bind:value={selectedField}
                testId="media-field"
                options={textFields}
              />
            </label>
            <!-- 上传控件：外层已是笔记表单，不能嵌套 form；用按钮点击触发，成功走与选择器同一段 insertMedia。 -->
            <div class="rounded-xl border border-zinc-200 dark:border-zinc-700 p-4 space-y-3">
              <label class="block text-sm font-medium">{$t('media.upload.label')}
                <input data-testid="media-upload-input" bind:this={uploadInput} type="file" name="file" class="mt-2 block w-full text-sm" />
              </label>
              <Button variant="outline" size="lg" testId="media-upload-submit" disabled={uploading} onclick={uploadMedia}>
                {uploading ? $t('media.upload.uploading') : $t('media.upload.button')}
              </Button>
              {#if uploaded}<p role="status" data-testid="media-upload-status" class="text-sm text-emerald-700 dark:text-emerald-400">{$t('media.upload.inserted')}</p>{/if}
              {#if uploadErrorKey}<p role="alert" data-testid="media-upload-error" class="text-sm text-rose-600 dark:text-rose-400">{$t(uploadErrorKey)}</p>{/if}
            </div>
            <!-- 媒体库选择器：数据走 GET /api/v1/media 的 JSON；选中后与上传共用同一段 insertMedia。 -->
            <MediaPicker onselect={insertMedia} />
          </Panel>
        </div>

        <!-- 右栏：自动预览。随输入、题型与插入变化重排；桌面吸顶，移动端顺排在输入下方。 -->
        <div class="lg:sticky lg:top-6">
          <Panel padding="md" class="space-y-3">
            <h2 class="text-lg font-semibold text-zinc-900 dark:text-zinc-100">{$t('note_preview.title')}</h2>
            <!-- 状态文案独占一个 polite 活区：只播报「生成中/不全/失败」这类短句，
                 卡片正文放在活区之外，避免每次重排把整段 HTML 重复念一遍。 -->
            <div aria-live="polite" class="space-y-3">
              {#if previewStatus === 'empty'}
                <p data-testid="note-preview-empty" class="text-sm text-zinc-500 dark:text-zinc-400">{$t('note_preview.empty')}</p>
              {:else if previewStatus === 'incomplete'}
                <p data-testid="note-preview-incomplete" role="status" class="text-sm text-amber-700 dark:text-amber-400">{$t('note_preview.incomplete')}</p>
              {:else if previewStatus === 'error'}
                <p data-testid="note-preview-error" role="alert" class="text-sm text-rose-600 dark:text-rose-400">{$t('note_preview.failed')}</p>
              {:else if previewStatus === 'loading' && !previewCards}
                <p data-testid="note-preview-loading" role="status" class="text-sm text-zinc-500 dark:text-zinc-400">{$t('note_preview.loading')}</p>
              {/if}

              {#if previewStatus === 'loading' && previewCards}
                <p data-testid="note-preview-updating" role="status" class="text-sm text-zinc-400 dark:text-zinc-500">{$t('note_preview.updating')}</p>
              {/if}
            </div>

            {#if previewCards}
              <!-- 保留上一份结果时压暗并置 aria-busy，让「陈旧但仍在重排」看得见而不是闪空。 -->
              <div
                bind:this={previewSection}
                data-testid="note-preview-result"
                aria-busy={previewStatus === 'loading'}
                class="space-y-3 transition-opacity {previewStatus === 'loading' ? 'opacity-60' : ''}"
              >
                {#each previewCards as card, index}
                  <Panel padding="sm" class="space-y-3">
                    <div data-testid="note-preview-front-{index}">
                      <h3 class="text-sm font-medium text-zinc-700 dark:text-zinc-300">{$t('note_preview.front')}</h3>
                      <!-- 仅使用预览 API 返回的 HTML；后端通过 RenderMarkdown 与 bluemonday 清理，禁止将编辑器原始字段传入 HTML sink。 -->
                      <div class="prose dark:prose-invert">{@html card.front_html}</div>
                    </div>
                    <div data-testid="note-preview-back-{index}">
                      <h3 class="text-sm font-medium text-zinc-700 dark:text-zinc-300">{$t('note_preview.back')}</h3>
                      <!-- 仅使用预览 API 返回的 HTML；后端通过 RenderMarkdown 与 bluemonday 清理，禁止将编辑器原始字段传入 HTML sink。 -->
                      <div class="prose dark:prose-invert">{@html card.back_html}</div>
                    </div>
                  </Panel>
                {/each}
              </div>
            {/if}
          </Panel>
        </div>
      </div>

      <div class="mt-5 flex items-center gap-3 flex-wrap">
        <Button type="submit" size="lg" testId="note-submit" disabled={saving}>
          {saving
            ? (isCreate ? $t('note_create.saving') : $t('note_edit.saving'))
            : (isCreate ? $t('note_create.submit') : $t('note_edit.save'))}
        </Button>
        <Button variant="ghost" size="lg" testId="note-cancel" onclick={() => navigate(`/decks/${encodeURIComponent(deckId)}`)}>
          {$t('note_edit.cancel')}
        </Button>
        {#if invalid}<p role="alert" class="text-sm text-rose-600 dark:text-rose-400">{isCreate ? $t('note_create.invalid') : $t('note_edit.invalid')}</p>{/if}
        {#if saveError}<p role="alert" data-testid="note-save-error" class="text-sm text-rose-600 dark:text-rose-400">{isCreate ? $t('note_create.failed') : $t('note_edit.failed')}</p>{/if}
        {#if saved}<p role="status" data-testid="note-saved" class="text-sm text-emerald-700 dark:text-emerald-400">{$t('note_edit.saved')}</p>{/if}
      </div>
    </form>
  {/if}
</section>
