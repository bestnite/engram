<script lang="ts">
  import Skeleton from '../components/ui/Skeleton.svelte';
  import Page from '../components/ui/Page.svelte';
  import { onMount } from 'svelte';
  import { routeStore } from '../router';
  import { t } from '../i18n';
  import { apiClient, ApiClientError } from '../api';
  import Select from '../components/ui/Select.svelte';
  import Button from '../components/ui/Button.svelte';
  import Badge from '../components/ui/Badge.svelte';
  import SettingsSection from '../components/ui/SettingsSection.svelte';
  import { listClasses } from '../components/ui/variants';
  import { toast } from '../components/ui/toast';
  import { UserMinus, X } from '@lucide/svelte';

  // When embedded is true this component sits inside the deck details' "sharing" tab: it does not render the page-level width/padding shell,
  // nor the page header title (the tab already indicates the current section). It is false when /decks/:id/sharing is visited directly, standing alone as a page.
  interface Props {
    embedded?: boolean;
  }
  let { embedded = false }: Props = $props();

  type Grant = { user_id: string; username: string; role: 'reader' | 'editor' };
  // Invitations pending acceptance: the owner must be able to tell "already granted" from "invited but not yet accepted" — the latter can be declined at any time,
  // and the UI should not present it as existing access (consent model).
  type Invite = { user_id: string; username: string; role: 'reader' | 'editor'; expires_at: string };
  type Sharing = { deck_id: string; deck_name: string; grants: Grant[]; pending_invites?: Invite[] };
  let data = $state<Sharing | null>(null);
  let loading = $state(true);
  let failed = $state(false);
  let saving = $state(false);
  // Which form's submission is in flight: only its button spins; saving still disables the remaining write operations along with it.
  let submitting = $state<'grant' | null>(null);
  let error = $state('');
  let username = $state('');
  let role = $state<'reader'|'editor'>('reader');
  const deckId = $derived($routeStore.params.id || '');
  const apiPath = $derived(`/api/v1/decks/${encodeURIComponent(deckId)}/sharing`);
  async function load(): Promise<void> {
    loading = true; failed = false;
    try { data = await apiClient.request<Sharing>(apiPath); }
    catch (e) { failed = true; error = e instanceof ApiClientError && e.isForbidden ? 'deck.sharing.forbidden' : 'deck.sharing.failed'; }
    finally { loading = false; }
  }
  async function change(method: string, path: string, body?: object): Promise<void> {
    saving = true; error = '';
    try { await apiClient.request<unknown>(path, { method, ...(body ? { body: JSON.stringify(body) } : {}) }); await load(); }
    catch (e) {
      // The other party set "do not accept shares": that is an explainable outcome, not a failure (the server's 409 recipient_refuses_shares).
      if (e instanceof ApiClientError && e.code === 'recipient_refuses_shares') error = 'deck.sharing.refused';
      else if (e instanceof ApiClientError && e.isForbidden) error = 'deck.sharing.forbidden';
      else error = 'deck.sharing.failed';
      toast.error($t(error));
    }
    finally { saving = false; }
  }
  async function grant(event: SubmitEvent): Promise<void> { event.preventDefault(); submitting = 'grant'; await change('POST', `${apiPath}/grants`, { username: username.trim(), role }); submitting = null; if (!error) username = ''; }
  const roleOptions = $derived([{ value: 'reader', label: $t('deck.sharing.role.reader') }, { value: 'editor', label: $t('deck.sharing.role.editor') }]);
  onMount(() => { void load(); });
</script>

<Page {embedded} testId="deck-sharing-view">
  {#if loading}<Skeleton testId="deck-sharing-loading" label={$t('common.loading')} />
  {:else if failed}<p class="py-16 text-center text-sm text-destructive-foreground" role="alert">{$t(error)}</p>
  {:else if data}
    {#if !embedded}<h1 class="mb-6 text-2xl font-semibold tracking-tight text-foreground">{$t('deck.sharing.title')}: {data.deck_name}</h1>{/if}

    <SettingsSection title={$t('deck.sharing.grants')} description={$t('deck.sharing.grants_hint')} testId="sharing-members">
      <form class="flex flex-wrap items-end gap-2" onsubmit={grant}>
        <label class="min-w-48 flex-1 text-sm font-medium text-foreground">{$t('deck.sharing.username')}
          <input required bind:value={username} class="field-input mt-1.5 block w-full text-sm" />
        </label>
        <Select class="w-28" value={role} onValueChange={(value) => (role = value as typeof role)} ariaLabel={$t('deck.sharing.role_label')} options={roleOptions} />
        <Button type="submit" size="lg" loading={submitting === 'grant'} disabled={saving} testId="sharing-grant">{$t('deck.sharing.grant')}</Button>
      </form>

      {#if data.grants.length > 0 || (data.pending_invites ?? []).length > 0}
        <ul class="{listClasses.root} mt-4">
          {#each data.grants as g (g.user_id)}
            <li class="flex items-center gap-3 border-b border-border px-4 py-2.5 last:border-b-0">
              <span class="min-w-0 flex-1 truncate text-sm text-foreground">{g.username}</span>
              <Select class="w-28" size="sm" value={g.role} onValueChange={(next) => change('POST', `${apiPath}/grants`, { user_id: g.user_id, role: next })} ariaLabel={$t('deck.sharing.role_label')} options={roleOptions} />
              <Button variant="ghost" size="icon" class="hover:bg-destructive-soft hover:text-destructive-foreground" label={$t('deck.sharing.revoke')} title={$t('deck.sharing.revoke')} disabled={saving} onclick={() => change('DELETE', `${apiPath}/grants/${g.user_id}`)}>
                <UserMinus class="size-4" aria-hidden="true" />
              </Button>
            </li>
          {/each}
          <!-- Pending acceptance: not a member yet, access only exists after the other party accepts (consent model). Canceling goes through the same revoke endpoint. -->
          {#each data.pending_invites ?? [] as invite (invite.user_id)}
            <li class="flex items-center gap-3 border-b border-border px-4 py-2.5 last:border-b-0" data-testid="sharing-pending-invites" title={$t('deck.sharing.pending_hint')}>
              <span class="min-w-0 flex-1 truncate text-sm text-muted-foreground">{invite.username}</span>
              <Badge variant="warning">{$t('deck.sharing.pending_title')}</Badge>
              <span class="w-28 text-[13px] text-muted-foreground">{$t(`deck.sharing.role.${invite.role}`)}</span>
              <Button variant="ghost" size="icon" label={$t('deck.sharing.revoke')} title={$t('deck.sharing.revoke')} disabled={saving} onclick={() => change('DELETE', `${apiPath}/grants/${invite.user_id}`)}>
                <X class="size-4" aria-hidden="true" />
              </Button>
            </li>
          {/each}
        </ul>
      {/if}
    </SettingsSection>
  {/if}
</Page>
