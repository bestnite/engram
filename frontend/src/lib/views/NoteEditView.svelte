<script lang="ts">
  import { onMount } from 'svelte';
  import { routeStore, navigate } from '../router';
  import { t } from '../i18n';
  import { apiClient, ApiClientError } from '../api';
  import type { Note } from '../api';

  let note = $state<Note | null>(null);
  let fieldsText = $state('');
  let tagsText = $state('');
  let loading = $state(true);
  let saving = $state(false);
  let error = $state(false);
  let invalid = $state(false);
  let saved = $state(false);
  const deckId = $derived($routeStore.params.id || '');
  const noteId = $derived($routeStore.params.noteId || '');

  async function load(): Promise<void> {
    loading = true;
    error = false;
    try {
      // 列表返回字段保持纯文本；编辑器不把 Markdown 当 HTML 渲染。
      const result = await apiClient.getDeckNotes(deckId, { page: 1, per_page: 100 });
      note = result.notes.find((item) => String(item.id) === noteId) || null;
      if (note) {
        fieldsText = JSON.stringify(note.fields, null, 2);
        tagsText = note.tags.join(', ');
      } else error = true;
    } catch {
      error = true;
    } finally {
      loading = false;
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

<section class="py-10 max-w-3xl mx-auto px-4">
  <a href="/decks/{deckId}" class="text-sm text-zinc-500 hover:text-zinc-900 dark:hover:text-zinc-100">← {$t('note_edit.back')}</a>
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
