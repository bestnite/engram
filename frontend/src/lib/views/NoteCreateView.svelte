<script lang="ts">
  import { routeStore, navigate } from '../router';
  import { t } from '../i18n';
  import { apiClient, ApiClientError } from '../api';
  import Button from '../components/ui/Button.svelte';

  let front = $state('');
  let back = $state('');
  let tagsText = $state('');
  let saving = $state(false);
  let saved = $state(false);
  let errorKey = $state<string | null>(null);
  const deckId = $derived($routeStore.params.id || '');

  async function create(event: SubmitEvent): Promise<void> {
    event.preventDefault();
    saving = true;
    saved = false;
    errorKey = null;
    try {
      const result = await apiClient.createNotes(deckId, {
        notes: [{
          kind: 'basic',
          fields: { front: front.trim(), back: back.trim() },
          tags: tagsText.split(',').map((tag) => tag.trim()).filter(Boolean),
        }],
      });
      if (result.errors.length > 0 || result.created !== 1) {
        errorKey = 'note_create.failed';
        return;
      }
      saved = true;
      front = '';
      back = '';
      tagsText = '';
    } catch (err) {
      errorKey = err instanceof ApiClientError && err.status === 400
        ? 'note_create.invalid'
        : 'note_create.failed';
    } finally {
      saving = false;
    }
  }
</script>

<section class="py-10 max-w-4xl mx-auto px-4">
  <a href="/decks/{encodeURIComponent(deckId)}" class="inline-flex items-center gap-1.5 text-sm text-zinc-500 hover:text-zinc-900 dark:text-zinc-400 dark:hover:text-zinc-100 transition-colors">
    <svg class="w-4 h-4" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round" aria-hidden="true"><polyline points="15 18 9 12 15 6"/></svg>
    <span>{$t('note_create.back_to_deck')}</span>
  </a>
  <h1 class="text-2xl font-bold my-5">{$t('note_create.title')}</h1>
  <form onsubmit={create} class="card-elevated rounded-xl p-6 space-y-4" data-testid="note-create-form">
    <label class="block text-sm font-medium text-zinc-700 dark:text-zinc-300">
      {$t('note_create.front')}
      <textarea data-testid="note-create-front" bind:value={front} required rows="4" class="field-input text-sm mt-1 w-full"></textarea>
    </label>
    <label class="block text-sm font-medium text-zinc-700 dark:text-zinc-300">
      {$t('note_create.back_field')}
      <textarea data-testid="note-create-back" bind:value={back} required rows="4" class="field-input text-sm mt-1 w-full"></textarea>
    </label>
    <label class="block text-sm font-medium text-zinc-700 dark:text-zinc-300">
      {$t('note_create.tags')}
      <input data-testid="note-create-tags" bind:value={tagsText} class="field-input text-sm mt-1 w-full" />
    </label>
    {#if errorKey}<p role="alert" data-testid="note-create-error" class="text-rose-600 dark:text-rose-400">{$t(errorKey)}</p>{/if}
    {#if saved}<p role="status" data-testid="note-create-success" class="text-emerald-700 dark:text-emerald-400">{$t('note_create.saved')}</p>{/if}
    <div class="flex gap-3">
      <Button type="submit" testId="note-create-submit" disabled={saving || !front.trim() || !back.trim()}>{saving ? $t('note_create.saving') : $t('note_create.submit')}</Button>
      <Button variant="ghost" onclick={() => navigate(`/decks/${encodeURIComponent(deckId)}`)}>{$t('note_create.cancel')}</Button>
    </div>
  </form>
</section>
