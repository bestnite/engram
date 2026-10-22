<script lang="ts">
  import Page from '../components/ui/Page.svelte';
  import { onMount } from 'svelte';
  import { t } from '../i18n';
  import { apiClient, ApiClientError } from '../api';
  import type { ShareResponse } from '../api';
  import { authStore } from '../auth';
  import { routeStore } from '../router';
  import Skeleton from '../components/ui/Skeleton.svelte';
  import Button from '../components/ui/Button.svelte';
  import { toast } from '../components/ui/toast';
  import PageHeader from '../components/ui/PageHeader.svelte';

  // Public read-only share browsing page (after the server's GET /s/:token switched to the shell, the client router renders this page).
  // Card front and back are always server-sanitized HTML; this only injects the sanitized result as markup and never renders the raw fields as Markdown.
  // The visibility of media (<img src="/media/<sha>">) is still decided by the server: only a session that is logged in and has opened that deck is let through.
  //
  // Opening a link only records "this session has seen it"; it does not add the deck to the visitor's list — links get forwarded,
  // and clicking one in passing should not equal agreeing to receive a deck. Joining is the explicit action below ("add to my decks").
  const token = $derived($routeStore.params.token ?? '');

  let status = $state<'loading' | 'password' | 'content' | 'error'>('loading');
  let share = $state<ShareResponse | null>(null);
  let password = $state('');
  let unlocking = $state(false);
  let errorKey = $state<string | null>(null);

  // Join state: the button spins while joining; after joined the join button becomes "open deck", and the outcome goes through toast.
  let joining = $state(false);
  let joined = $state(false);
  let joinedDeckId = $state<number | null>(null);

  // Map the share API's stable error codes to share.* language pack keys; never echo the backend's English message.
  function mapShareError(err: unknown): string {
    if (err instanceof ApiClientError) {
      if (err.code === 'share_password_invalid') return 'share.error_password';
      if (err.isNotFound) return 'share.error_not_found';
      if (err.isNetworkError) return 'error.network';
    }
    return 'share.error_not_found';
  }

  async function loadShare(): Promise<void> {
    status = 'loading';
    try {
      const res = await apiClient.getShare(token);
      share = res;
      status = res.password_required ? 'password' : 'content';
    } catch (err) {
      errorKey = mapShareError(err);
      status = 'error';
    }
  }

  async function handleUnlock(e: SubmitEvent): Promise<void> {
    e.preventDefault();
    unlocking = true;
    try {
      const res = await apiClient.unlockShare(token, password);
      share = res;
      status = 'content';
    } catch (err) {
      toast.error($t(mapShareError(err)));
    } finally {
      unlocking = false;
    }
  }

  // Join: add the deck the link points at to your own list. When the link is invalid/revoked the server returns 404, reported with the same set of share.* copy.
  async function join(): Promise<void> {
    joining = true;
    try {
      const res = await apiClient.joinSharedDeck(token);
      joined = true;
      joinedDeckId = res.deck_id;
      toast.success($t('share.browse.joined'));
    } catch (err) {
      toast.error($t(
        err instanceof ApiClientError && err.isNotFound
          ? 'share.error_not_found'
          : err instanceof ApiClientError && err.isNetworkError
            ? 'error.network'
            : 'share.browse.join_failed'
      ));
    } finally {
      joining = false;
    }
  }

  onMount(loadShare);
</script>

<Page>
  <PageHeader title={$t('share.browse.heading')}>
    {#snippet meta()}
      {#if share}
        <p data-testid="share-deck-name" class="text-base font-medium text-foreground">{share.deck_name}</p>
        <p class="mt-0.5 text-sm text-muted-foreground">{$t('share.browse.intro')}</p>
      {/if}
    {/snippet}
    {#snippet actions()}
      {#if share && status !== 'loading' && status !== 'error' && status !== 'password'}
        {#if $authStore.authenticated}
          {#if joined}
            {#if joinedDeckId !== null}
              <Button testId="share-open-deck" variant="primary" size="lg" href={`/decks/${joinedDeckId}`}>{$t('share.browse.open_deck')}</Button>
            {/if}
          {:else}
            <Button variant="primary" size="lg" loading={joining} onclick={join} testId="share-join">
              {$t('share.browse.join')}
            </Button>
          {/if}
        {:else}
          <Button variant="primary" size="lg" href="/login">{$t('share.browse.login')}</Button>
        {/if}
      {/if}
    {/snippet}
  </PageHeader>

  {#if status === 'loading'}
    <Skeleton testId="share-loading" label={$t('share.browse.loading')} lines={2} />
  {:else if status === 'error'}
    <p data-testid="share-error" role="alert" class="py-16 text-center text-sm text-destructive-foreground">
      {errorKey ? $t(errorKey) : $t('share.error_not_found')}
    </p>
  {:else if status === 'password'}
    <form onsubmit={handleUnlock} class="max-w-sm space-y-4">
      <p class="text-sm text-muted-foreground">{$t('share.password.intro')}</p>
      <div>
        <label for="share-password" class="block text-sm font-medium text-foreground">{$t('share.password.label')}</label>
        <input
          id="share-password"
          data-testid="share-password"
          name="password"
          type="password"
          autocomplete="current-password"
          required
          bind:value={password}
          disabled={unlocking}
          placeholder={$t('share.password.placeholder')}
          class="field-input mt-1.5 w-full text-sm disabled:opacity-50"
        />
      </div>
      <Button type="submit" loading={unlocking} disabled={!password} variant="primary" size="lg" testId="share-browse-submit">
        {$t('share.password.submit')}
      </Button>
    </form>
  {:else if share}
    {#if share.notes.length === 0}
      <p data-testid="share-empty" class="rounded-lg border border-dashed border-border py-16 text-center text-sm text-muted-foreground">{$t('share.browse.empty')}</p>
    {:else}
      <!-- Card preview: one card per row, front on the left and back on the right, no longer cards stacked one by one vertically. -->
      <div class="overflow-hidden rounded-lg border border-border">
        <div class="hidden grid-cols-2 border-b border-border bg-surface text-xs text-muted-foreground sm:grid">
          <div class="px-4 py-2.5">{$t('share.browse.front')}</div>
          <div class="border-l border-border px-4 py-2.5">{$t('share.browse.back')}</div>
        </div>
        <ul class="divide-y divide-border">
          {#each share.notes as note, i}
            <li data-testid="share-note" class="grid sm:grid-cols-2">
              <div class="px-4 py-3 text-sm leading-relaxed text-foreground">
                <span class="mb-1 block text-xs text-muted-foreground sm:hidden">{$t('share.browse.front')}</span>
                {@html note.front_html}
              </div>
              <div class="border-border px-4 py-3 text-sm leading-relaxed text-foreground/80 max-sm:border-t sm:border-l">
                <span class="mb-1 block text-xs text-muted-foreground sm:hidden">{$t('share.browse.back')}</span>
                {@html note.back_html}
              </div>
              <span class="sr-only">{i + 1}</span>
            </li>
          {/each}
        </ul>
      </div>
    {/if}
  {/if}
</Page>
