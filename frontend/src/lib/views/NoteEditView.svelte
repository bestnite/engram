<script lang="ts">
  import { onMount, tick } from 'svelte';
  import { routeStore, navigate } from '../router';
  import { t } from '../i18n';
  import { apiClient, ApiClientError } from '../api';
  import type { Note } from '../api';
  import { typeset } from '../mathjax';
  import MediaPicker from '../components/MediaPicker.svelte';
  import Select from '../components/ui/Select.svelte';

  let note = $state<Note | null>(null);
  let fieldsText = $state('');
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

  async function load(): Promise<void> {
    loading = true;
    error = false;
    try {
      // 列表返回字段保持纯文本；编辑器不把 Markdown 当 HTML 渲染。
      const result = await apiClient.getDeckNotes(deckId, { page: 1, per_page: 100 });
      const loadedNote = result.notes.find((item) => String(item.id) === noteId) || null;
      note = loadedNote;
      if (loadedNote) {
        fieldsText = JSON.stringify(loadedNote.fields, null, 2);
        tagsText = loadedNote.tags.join(', ');
        selectedField = Object.keys(loadedNote.fields).find((key) => typeof loadedNote.fields[key] === 'string') || '';
      } else error = true;
    } catch {
      error = true;
    } finally {
      loading = false;
    }
  }

  // insertMedia 把 Markdown 图片引用追加到当前选中字段；上传与选择器共用同一段逻辑。
  // 只做字符串拼接，绝不把 url 当 HTML 渲染；返回是否真的插入了（字段非法时不动内容）。
  function insertMedia(url: string): boolean {
    let fields: Record<string, unknown>;
    try {
      const parsed: unknown = JSON.parse(fieldsText);
      if (!parsed || typeof parsed !== 'object' || Array.isArray(parsed)) throw new Error('fields');
      fields = parsed as Record<string, unknown>;
    } catch {
      invalid = true;
      return false;
    }
    const current = fields[selectedField];
    if (typeof current !== 'string') return false;
    fields[selectedField] = `${current}${current && !current.endsWith('\n') ? '\n' : ''}![](${url})`;
    fieldsText = JSON.stringify(fields, null, 2);
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

  async function preview(): Promise<void> {
    invalid = false;
    previewError = false;
    let fields: Record<string, unknown>;
    try {
      const parsed: unknown = JSON.parse(fieldsText);
      if (!parsed || typeof parsed !== 'object' || Array.isArray(parsed)) throw new Error('fields');
      fields = parsed as Record<string, unknown>;
    } catch {
      invalid = true;
      previewCards = null;
      return;
    }
    if (!note) return;
    previewLoading = true;
    previewCards = null;
    try {
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
    let fields: Record<string, unknown>;
    try {
      const parsed: unknown = JSON.parse(fieldsText);
      if (!parsed || typeof parsed !== 'object' || Array.isArray(parsed)) throw new Error('fields');
      fields = parsed as Record<string, unknown>;
    } catch {
      invalid = true;
      return;
    }
    if (!note) return;
    saving = true;
    saved = false;
    try {
      const updated = await apiClient.updateNote(note.id, {
        kind: note.kind,
        fields,
        tags: tagsText.split(',').map((tag) => tag.trim()).filter(Boolean),
      });
      note = updated;
      fieldsText = JSON.stringify(updated.fields, null, 2);
      tagsText = updated.tags.join(', ');
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
    <p>{$t('note_edit.loading')}</p>
  {:else if error && !note}
    <p role="alert">{$t('note_edit.not_found')}</p>
  {:else if note}
    <form onsubmit={save} class="card-elevated rounded-xl p-6 space-y-4">
      <label class="block text-sm font-medium">{$t('note_edit.fields')}
        <textarea data-testid="note-fields-editor" bind:value={fieldsText} rows="14" spellcheck="false" class="mt-2 w-full rounded-md border border-zinc-300 dark:border-zinc-700 bg-white dark:bg-zinc-900 p-3 font-mono text-sm"></textarea>
      </label>
      <!-- 选择器只引用原图；延迟加载与限高遵循 DESIGN.md §6.3。 -->
      <div class="space-y-3">
        <label class="block text-sm font-medium">{$t('media.spa.field')}
          <Select
            class="ml-2 w-40"
            bind:value={selectedField}
            testId="spa-media-field"
            options={Object.entries(note.fields)
              .filter(([, value]) => typeof value === 'string')
              .map(([key]) => ({ value: key, label: key }))}
          />
        </label>
        <!-- 上传控件：外层已是笔记表单，不能嵌套 form；用按钮点击触发，成功走与选择器同一段 insertMedia。 -->
        <div class="rounded-xl border border-zinc-200 dark:border-zinc-700 p-4 space-y-3">
          <label class="block text-sm font-medium">{$t('media.spa.upload.label')}
            <input data-testid="spa-media-upload-input" bind:this={uploadInput} type="file" name="file" class="mt-2 block w-full text-sm" />
          </label>
          <button data-testid="spa-media-upload-submit" type="button" onclick={uploadMedia} disabled={uploading} class="rounded-md border px-4 py-2 disabled:opacity-50">{uploading ? $t('media.spa.upload.uploading') : $t('media.spa.upload.button')}</button>
          {#if uploaded}<p role="status" data-testid="spa-media-upload-status" class="text-emerald-700">{$t('media.spa.upload.inserted')}</p>{/if}
          {#if uploadErrorKey}<p role="alert" data-testid="spa-media-upload-error" class="text-rose-600">{$t(uploadErrorKey)}</p>{/if}
        </div>
        <!-- 媒体库选择器：数据走 GET /api/v1/media 的 JSON；选中后与上传共用同一段 insertMedia。 -->
        <MediaPicker onselect={insertMedia} />
      </div>
      <button data-testid="note-preview" type="button" onclick={preview} disabled={previewLoading} class="rounded-md border px-4 py-2 disabled:opacity-50">{previewLoading ? $t('note_preview.spa.loading') : $t('note_preview.spa.action')}</button>
      {#if previewError}<p role="alert" data-testid="note-preview-error">{$t('note_preview.spa.failed')}</p>{/if}
      {#if previewCards}
        <section bind:this={previewSection} data-testid="note-preview-result" class="space-y-3">
          <h2 class="text-lg font-semibold">{$t('note_preview.spa.title')}</h2>
          <p class="text-sm text-zinc-500">{$t('note_preview.spa.math_notice')}</p>
          {#each previewCards as card, index}
            <article class="rounded-md border p-4 space-y-3">
              <div data-testid="note-preview-front-{index}">
                <h3 class="text-sm font-medium">{$t('note_preview.spa.front')}</h3>
                <!-- 仅使用预览 API 返回的 HTML；后端通过 RenderMarkdown 与 bluemonday 清理，禁止将编辑器原始字段传入 HTML sink。 -->
                <div class="prose dark:prose-invert">{@html card.front_html}</div>
              </div>
              <div data-testid="note-preview-back-{index}">
                <h3 class="text-sm font-medium">{$t('note_preview.spa.back')}</h3>
                <!-- 仅使用预览 API 返回的 HTML；后端通过 RenderMarkdown 与 bluemonday 清理，禁止将编辑器原始字段传入 HTML sink。 -->
                <div class="prose dark:prose-invert">{@html card.back_html}</div>
              </div>
            </article>
          {/each}
        </section>
      {/if}
      <label class="block text-sm font-medium">{$t('note_edit.tags')}
        <input data-testid="note-tags-editor" bind:value={tagsText} class="mt-2 w-full rounded-md border border-zinc-300 dark:border-zinc-700 bg-white dark:bg-zinc-900 p-2" />
      </label>
      {#if invalid}<p role="alert" class="text-rose-600">{$t('note_edit.invalid')}</p>{/if}
      {#if error}<p role="alert" class="text-rose-600">{$t('note_edit.failed')}</p>{/if}
      {#if saved}<p role="status" class="text-emerald-700">{$t('note_edit.saved')}</p>{/if}
      <div class="flex gap-3">
        <button data-testid="note-save" type="submit" disabled={saving} class="rounded-md bg-zinc-900 text-white px-4 py-2 disabled:opacity-50">{saving ? $t('note_edit.saving') : $t('note_edit.save')}</button>
        <button type="button" onclick={() => navigate(`/decks/${encodeURIComponent(deckId)}`)} class="rounded-md px-4 py-2">{$t('note_edit.cancel')}</button>
      </div>
    </form>
  {/if}
</section>
