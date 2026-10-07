<script lang="ts">
  import { onMount, tick } from 'svelte';
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
  import {
    CARD_KIND_FIELDS,
    emptyFields,
    fieldsForKind,
    toFormFields,
    toPayloadFields,
  } from '../card-fields';

  // 编辑器的字段模型按题型走字段表（card-fields.ts），界面不再暴露 JSON：
  // 每个题型有各自的字段与控件形态，用户看到的是「正面/背面」而不是键值对。
  let note = $state<Note | null>(null);
  let kind = $state('basic');
  let fields = $state<Record<string, unknown>>({});
  // 该题型字段表之外的键（例如外部写入的自定义键）原样保留，保存时合回，绝不静默丢弃。
  let unknownKeys = $state<Record<string, unknown>>({});
  let tagsText = $state('');
  let loading = $state(true);
  let saving = $state(false);
  let error = $state(false);
  let invalid = $state(false);
  let saved = $state(false);
  let previewLoading = $state(false);
  let previewError = $state(false);
  let previewCards = $state<Array<{ front_html: string; back_html: string }> | null>(null);
  // 预览结果容器：只对它调用 MathJax 排版，绝不整页排版编辑器原始 Markdown。
  let previewSection = $state<HTMLElement | null>(null);
  let selectedField = $state('');
  // 上传控件状态：与媒体库选择器共用 insertMedia 的插入路径（DESIGN.md §6.3 编辑器媒体面）。
  let uploadInput = $state<HTMLInputElement | null>(null);
  let uploading = $state(false);
  let uploadErrorKey = $state('');
  let uploaded = $state(false);
  const deckId = $derived($routeStore.params.id || '');
  const noteId = $derived($routeStore.params.noteId || '');

  const kindOptions = $derived(
    Object.keys(CARD_KIND_FIELDS).map((value) => ({ value, label: $t(`notes.kind.${value}` as never) }))
  );

  // 可插入媒体的字段：只有文本类字段能承载 Markdown 图片引用。
  const textFields = $derived(
    fieldsForKind(kind)
      .filter((spec) => spec.control === 'text' || spec.control === 'textarea' || spec.control === 'lines')
      .map((spec) => ({ value: spec.key, label: $t('note.fields.' + spec.key) }))
  );

  function firstTextField(nextKind: string): string {
    const spec = fieldsForKind(nextKind).find(
      (item) => item.control === 'text' || item.control === 'textarea' || item.control === 'lines'
    );
    return spec ? spec.key : '';
  }

  /** 载入卡片并把服务端字段收进表单模型；返回未知键另存，供保存时合并。 */
  function adoptFields(loaded: Note): void {
    kind = loaded.kind;
    fields = toFormFields(loaded.kind, loaded.fields);
    const known = new Set(fieldsForKind(loaded.kind).map((spec) => spec.key));
    unknownKeys = Object.fromEntries(Object.entries(loaded.fields).filter(([key]) => !known.has(key)));
    tagsText = loaded.tags.join(', ');
    selectedField = firstTextField(loaded.kind);
  }

  async function load(): Promise<void> {
    loading = true;
    error = false;
    try {
      // 列表返回字段保持纯文本；编辑器不把 Markdown 当 HTML 渲染。
      const result = await apiClient.getDeckNotes(deckId, { page: 1, per_page: 100 });
      const loadedNote = result.notes.find((item) => String(item.id) === noteId) || null;
      note = loadedNote;
      if (loadedNote) adoptFields(loadedNote);
      else error = true;
    } catch {
      error = true;
    } finally {
      loading = false;
    }
  }

  /**
   * 切换题型：按新题型重新初始化字段（旧字段名不通用，保留只会造成半张卡）。
   * 同时把 note.kind 同步过去，预演与保存都读它——预览端点按 kind 决定怎么渲染。
   */
  function changeKind(next: string): void {
    kind = next;
    fields = emptyFields(next);
    if (note) note.kind = next;
    selectedField = firstTextField(next);
    previewCards = null;
    saved = false;
    invalid = false;
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
  // 前端不解析后端英文 message（DESIGN.md §8.3）。
  function uploadErrorKeyFor(err: unknown): string {
    const code = err instanceof ApiClientError ? err.code : '';
    const map: Record<string, string> = {
      media_too_large: 'media.spa.upload.too_large',
      media_mime_not_allowed: 'media.spa.upload.mime_not_allowed',
      media_magic_mismatch: 'media.spa.upload.magic_mismatch',
      media_quota_exceeded: 'media.spa.upload.quota_exceeded',
      media_missing_file: 'media.spa.upload.file_required',
    };
    return map[code] || 'media.spa.upload.failed';
  }

  async function uploadMedia(): Promise<void> {
    uploaded = false;
    uploadErrorKey = '';
    const input = uploadInput;
    const file = input?.files?.[0];
    if (!file) {
      uploadErrorKey = 'media.spa.upload.file_required';
      return;
    }
    uploading = true;
    try {
      const result = await apiClient.uploadDeckMedia(deckId, file);
      uploaded = insertMedia(result.url);
      if (input) input.value = '';
    } catch (err) {
      uploadErrorKey = uploadErrorKeyFor(err);
    } finally {
      uploading = false;
    }
  }

  /** 表单模型 → 提交用字段：认识的键按表清洗，未知键原样带回。 */
  function collectFields(): Record<string, unknown> {
    return { ...unknownKeys, ...toPayloadFields(fields) };
  }

  async function preview(): Promise<void> {
    invalid = false;
    previewError = false;
    if (!note) return;
    const fields = collectFields();
    previewLoading = true;
    previewCards = null;
    try {
      // 直接使用卡片的当前题型标识，避免在此处再维护一份字段取值。
      const response = await apiClient.previewNote(deckId, note.kind, fields);
      previewCards = response.cards;
      // 等 Svelte 把服务端清洗后的 HTML 挂上 DOM，再只对这一容器排版公式。
      await tick();
      await typeset([previewSection]);
    } catch {
      previewError = true;
    } finally {
      previewLoading = false;
    }
  }

  async function save(event: SubmitEvent): Promise<void> {
    event.preventDefault();
    invalid = false;
    if (!note) return;
    saving = true;
    saved = false;
    try {
      const updated = await apiClient.updateNote(note.id, {
        kind: note.kind,
        fields: collectFields(),
        tags: tagsText.split(',').map((tag) => tag.trim()).filter(Boolean),
      });
      note = updated;
      adoptFields(updated);
      saved = true;
    } catch (err) {
      error = true;
      if (err instanceof ApiClientError) invalid = err.status === 400;
    } finally {
      saving = false;
    }
  }

  onMount(load);
</script>

<section class="py-10 max-w-4xl mx-auto px-4">
  <a href="/decks/{deckId}" title={$t('note_edit.back')} class="inline-flex items-center gap-1.5 text-sm text-zinc-500 hover:text-zinc-900 dark:text-zinc-400 dark:hover:text-zinc-100 transition-colors">
    <svg class="w-4 h-4" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round" aria-hidden="true"><polyline points="15 18 9 12 15 6"/></svg>
    <span>{$t('note_edit.back')}</span>
  </a>
  <h1 class="text-2xl font-bold my-5">{$t('note_edit.title', { id: noteId })}</h1>
  {#if loading}
    <p class="text-sm text-zinc-500 dark:text-zinc-400">{$t('note_edit.loading')}</p>
  {:else if error && !note}
    <p role="alert" class="text-sm text-rose-600 dark:text-rose-400">{$t('note_edit.not_found')}</p>
  {:else if note}
    <form onsubmit={save}>
      <Panel class="space-y-5">
        <div>
          <span class="block text-xs font-semibold uppercase tracking-wider text-zinc-500 dark:text-zinc-400">
            {$t('note_edit.kind')}
          </span>
          <Select
            class="mt-1.5 max-w-xs"
            testId="note-edit-kind"
            value={kind}
            onValueChange={changeKind}
            options={kindOptions}
          />
          <p class="mt-1 text-xs text-zinc-400 dark:text-zinc-500">{$t('note_edit.kind_hint')}</p>
        </div>

        <NoteFieldsForm specs={fieldsForKind(kind)} {fields} testIdPrefix="note-field" />

        <label class="block text-sm font-medium text-zinc-700 dark:text-zinc-300">{$t('note_edit.tags')}
          <input data-testid="note-tags-editor" bind:value={tagsText} class="mt-1.5 w-full rounded-xl border border-zinc-300 bg-white p-2 text-sm dark:border-zinc-700 dark:bg-zinc-900" />
        </label>
      </Panel>

      <!-- 选择器只引用原图；延迟加载与限高遵循 DESIGN.md §6.3。 -->
      <Panel padding="md" class="mt-5 space-y-3">
        <label class="block text-sm font-medium text-zinc-700 dark:text-zinc-300">{$t('media.spa.field')}
          <Select
            class="ml-2 w-52"
            bind:value={selectedField}
            testId="spa-media-field"
            options={textFields}
          />
        </label>
        <!-- 上传控件：外层已是笔记表单，不能嵌套 form；用按钮点击触发，成功走与选择器同一段 insertMedia。 -->
        <div class="rounded-xl border border-zinc-200 dark:border-zinc-700 p-4 space-y-3">
          <label class="block text-sm font-medium">{$t('media.spa.upload.label')}
            <input data-testid="spa-media-upload-input" bind:this={uploadInput} type="file" name="file" class="mt-2 block w-full text-sm" />
          </label>
          <Button variant="outline" size="lg" testId="spa-media-upload-submit" disabled={uploading} onclick={uploadMedia}>
            {uploading ? $t('media.spa.upload.uploading') : $t('media.spa.upload.button')}
          </Button>
          {#if uploaded}<p role="status" data-testid="spa-media-upload-status" class="text-sm text-emerald-700 dark:text-emerald-400">{$t('media.spa.upload.inserted')}</p>{/if}
          {#if uploadErrorKey}<p role="alert" data-testid="spa-media-upload-error" class="text-sm text-rose-600 dark:text-rose-400">{$t(uploadErrorKey)}</p>{/if}
        </div>
        <!-- 媒体库选择器：数据走 GET /api/v1/media 的 JSON；选中后与上传共用同一段 insertMedia。 -->
        <MediaPicker onselect={insertMedia} />
      </Panel>

      <div class="mt-5 flex items-center gap-3">
        <Button variant="outline" size="lg" testId="note-preview" disabled={previewLoading} onclick={preview}>
          {previewLoading ? $t('note_preview.spa.loading') : $t('note_preview.spa.action')}
        </Button>
        {#if invalid}<p role="alert" class="text-sm text-rose-600 dark:text-rose-400">{$t('note_edit.invalid')}</p>{/if}
        {#if error}<p role="alert" class="text-sm text-rose-600 dark:text-rose-400">{$t('note_edit.failed')}</p>{/if}
        {#if saved}<p role="status" class="text-sm text-emerald-700 dark:text-emerald-400">{$t('note_edit.saved')}</p>{/if}
      </div>

      {#if previewError}<p role="alert" data-testid="note-preview-error" class="mt-3 text-sm text-rose-600 dark:text-rose-400">{$t('note_preview.spa.failed')}</p>{/if}
      {#if previewCards}
        <section bind:this={previewSection} data-testid="note-preview-result" class="mt-5 space-y-3">
          <h2 class="text-lg font-semibold text-zinc-900 dark:text-zinc-100">{$t('note_preview.spa.title')}</h2>
          <p class="text-sm text-zinc-500 dark:text-zinc-400">{$t('note_preview.spa.math_notice')}</p>
          {#each previewCards as card, index}
            <Panel padding="sm" class="space-y-3">
              <div data-testid="note-preview-front-{index}">
                <h3 class="text-sm font-medium text-zinc-700 dark:text-zinc-300">{$t('note_preview.spa.front')}</h3>
                <!-- 仅使用预览 API 返回的 HTML；后端通过 RenderMarkdown 与 bluemonday 清理，禁止将编辑器原始字段传入 HTML sink。 -->
                <div class="prose dark:prose-invert">{@html card.front_html}</div>
              </div>
              <div data-testid="note-preview-back-{index}">
                <h3 class="text-sm font-medium text-zinc-700 dark:text-zinc-300">{$t('note_preview.spa.back')}</h3>
                <!-- 仅使用预览 API 返回的 HTML；后端通过 RenderMarkdown 与 bluemonday 清理，禁止将编辑器原始字段传入 HTML sink。 -->
                <div class="prose dark:prose-invert">{@html card.back_html}</div>
              </div>
            </Panel>
          {/each}
        </section>
      {/if}

      <div class="mt-5 flex gap-3">
        <Button type="submit" size="lg" testId="note-save" disabled={saving}>
          {saving ? $t('note_edit.saving') : $t('note_edit.save')}
        </Button>
        <Button variant="ghost" size="lg" onclick={() => navigate(`/decks/${encodeURIComponent(deckId)}`)}>
          {$t('note_edit.cancel')}
        </Button>
      </div>
    </form>
  {/if}
</section>
