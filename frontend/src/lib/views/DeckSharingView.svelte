<script lang="ts">
  import { onMount } from 'svelte';
  import { routeStore } from '../router';
  import { t } from '../i18n';
  import { apiClient, ApiClientError } from '../api';
  type Grant = { user_id: number; username: string; role: 'reader' | 'editor' };
  type Link = { prefix: string; created_at: string; expires_at: string | null; has_password: boolean; revoked: boolean; expired: boolean };
  type Sharing = { deck_id: number; deck_name: string; visibility: string; grants: Grant[]; links: Link[] };
  let data = $state<Sharing | null>(null);
  let loading = $state(true);
  let failed = $state(false);
  let saving = $state(false);
  let error = $state('');
  let username = $state('');
  let role = $state<'reader'|'editor'>('reader');
  let visibility = $state('private');
  let password = $state('');
  let expiresAt = $state('');
  let newLink = $state('');
  const deckId = $derived($routeStore.params.id || '');
  const apiPath = $derived(`/api/v1/decks/${encodeURIComponent(deckId)}/sharing`);
  async function load(): Promise<void> {
    loading = true; failed = false;
    try { data = await apiClient.request<Sharing>(apiPath); visibility = data.visibility; }
    catch (e) { failed = true; error = e instanceof ApiClientError && e.isForbidden ? 'deck.sharing.forbidden' : 'deck.sharing.failed'; }
    finally { loading = false; }
  }
  async function change(method: string, path: string, body?: object, showLink = false): Promise<void> {
    saving = true; error = ''; newLink = '';
    try { const result = await apiClient.request<{link?:string}>(path, { method, ...(body ? { body: JSON.stringify(body) } : {}) }); if (showLink && result?.link) newLink = result.link; await load(); }
    catch (e) { error = e instanceof ApiClientError && e.isForbidden ? 'deck.sharing.forbidden' : 'deck.sharing.failed'; }
    finally { saving = false; }
  }
  async function grant(event: SubmitEvent): Promise<void> { event.preventDefault(); await change('POST', `${apiPath}/grants`, { username: username.trim(), role }); if (!error) username = ''; }
  async function createLink(event: SubmitEvent): Promise<void> { event.preventDefault(); await change('POST', `${apiPath}/links`, { password, expires_at: expiresAt }, true); password = ''; expiresAt = ''; }
  onMount(() => { void load(); });
</script>

<div class="mx-auto max-w-4xl space-y-6 px-4 py-10" data-testid="deck-sharing-view">
  <a href="/decks/{encodeURIComponent(deckId)}/notes" class="text-sm underline">← {$t('deck.sharing.back')}</a>
  {#if loading}<p role="status">{$t('common.loading')}</p>
  {:else if failed}<p role="alert">{$t(error)}</p>
  {:else if data}
    <header><h1 class="text-2xl font-bold">{$t('deck.sharing.title')}: {data.deck_name}</h1></header>
    {#if error}<p role="alert">{$t(error)}</p>{/if}
    {#if newLink}<section class="rounded-xl border border-emerald-300 bg-emerald-50 p-4" role="status"><p>{$t('deck.sharing.link_once')}</p><a class="break-all underline" href={newLink}>{newLink}</a><button class="ml-3 underline" onclick={() => newLink=''}>{$t('deck.sharing.dismiss')}</button></section>{/if}
    <section class="card-subtle space-y-4 rounded-xl p-5"><h2 class="text-lg font-semibold">{$t('deck.sharing.grants')}</h2>
      <ul class="divide-y"><li class="flex items-center justify-between gap-3 py-3"><span>{data.deck_name}</span><span>{$t('deck.sharing.owner')}</span></li>{#each data.grants as grant (grant.user_id)}<li class="flex flex-wrap items-center justify-between gap-3 py-3"><span>{grant.username} · {$t(`deck.sharing.role.${grant.role}`)}</span><div class="flex gap-2"><select aria-label={$t('deck.sharing.role_label')} value={grant.role} onchange={(e) => change('POST',`${apiPath}/grants`,{user_id:grant.user_id,role:e.currentTarget.value})}><option value="reader">{$t('deck.sharing.role.reader')}</option><option value="editor">{$t('deck.sharing.role.editor')}</option></select><button class="text-rose-700 underline" disabled={saving} onclick={() => change('DELETE',`${apiPath}/grants/${grant.user_id}`)}>{$t('deck.sharing.revoke')}</button></div></li>{/each}</ul>
      <form class="flex flex-wrap items-end gap-3" onsubmit={grant}><label class="grid gap-1 text-sm">{$t('deck.sharing.username')}<input required bind:value={username} class="rounded-lg border px-3 py-2" /></label><label class="grid gap-1 text-sm">{$t('deck.sharing.role_label')}<select bind:value={role} class="rounded-lg border px-3 py-2"><option value="reader">{$t('deck.sharing.role.reader')}</option><option value="editor">{$t('deck.sharing.role.editor')}</option></select></label><button disabled={saving} class="rounded-lg bg-zinc-900 px-4 py-2 text-white">{$t('deck.sharing.grant')}</button></form>
    </section>
    <section class="card-subtle space-y-4 rounded-xl p-5"><h2 class="text-lg font-semibold">{$t('deck.sharing.visibility')}</h2><label class="grid max-w-sm gap-1 text-sm">{$t('deck.sharing.visibility_label')}<select bind:value={visibility} class="rounded-lg border px-3 py-2"><option value="private">{$t('deck.sharing.visibility.private')}</option><option value="unlisted">{$t('deck.sharing.visibility.unlisted')}</option><option value="public">{$t('deck.sharing.visibility.public')}</option></select></label><button disabled={saving} class="rounded-lg border px-4 py-2" onclick={() => change('PATCH',`${apiPath}/visibility`,{visibility})}>{$t('deck.sharing.save')}</button></section>
    <section class="card-subtle space-y-4 rounded-xl p-5"><h2 class="text-lg font-semibold">{$t('deck.sharing.links')}</h2><form class="flex flex-wrap items-end gap-3" onsubmit={createLink}><label class="grid gap-1 text-sm">{$t('deck.sharing.password')}<input type="password" bind:value={password} class="rounded-lg border px-3 py-2" /></label><label class="grid gap-1 text-sm">{$t('deck.sharing.expiry')}<input type="date" bind:value={expiresAt} class="rounded-lg border px-3 py-2" /></label><button disabled={saving} class="rounded-lg bg-zinc-900 px-4 py-2 text-white">{$t('deck.sharing.create_link')}</button></form><ul class="divide-y">{#each data.links as link (link.prefix)}<li class="flex flex-wrap items-center justify-between gap-3 py-3"><span class="font-mono">{link.prefix} · {$t(link.revoked ? 'deck.sharing.link_revoked' : link.expired ? 'deck.sharing.link_expired' : 'deck.sharing.link_active')}{#if link.has_password} · {$t('deck.sharing.password_set')}{/if}</span>{#if !link.revoked && !link.expired}<button class="text-rose-700 underline" disabled={saving} onclick={() => change('DELETE',`${apiPath}/links/revoke/${link.prefix}`)}>{$t('deck.sharing.revoke')}</button>{/if}</li>{/each}</ul><button disabled={saving || !data.links.some((link) => !link.revoked && !link.expired)} class="text-rose-700 underline" onclick={() => change('DELETE',`${apiPath}/links`)}>{$t('deck.sharing.revoke_all')}</button></section>
  {/if}
</div>
