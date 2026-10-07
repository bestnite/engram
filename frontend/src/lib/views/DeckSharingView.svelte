<script lang="ts">
  import { onMount } from 'svelte';
  import { routeStore } from '../router';
  import { t } from '../i18n';
  import { apiClient, ApiClientError } from '../api';
  import Select from '../components/ui/Select.svelte';
  import Button from '../components/ui/Button.svelte';

  // embedded 为真时本组件嵌在卡组详情的「共享」Tab 内：不渲染页面级宽度/内边距外壳，
  // 也不渲染页头标题（Tab 已表明当前区块）。直接访问 /decks/:id/sharing 时为假，独立成页。
  interface Props {
    embedded?: boolean;
  }
  let { embedded = false }: Props = $props();

  type Grant = { user_id: number; username: string; role: 'reader' | 'editor' };
  type Link = { prefix: string; created_at: string; expires_at: string | null; has_password: boolean; revoked: boolean; expired: boolean };
  // 待接受的邀请：属主必须能区分「已授权」与「邀请了还没答应」——后者随时可能被拒绝，
  // 界面上不该显示成已有访问权（DESIGN.md §5 同意制）。
  type Invite = { user_id: number; username: string; role: 'reader' | 'editor'; expires_at: string };
  type Sharing = { deck_id: number; deck_name: string; visibility: string; grants: Grant[]; pending_invites?: Invite[]; links: Link[] };
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
    catch (e) {
      // 对方设了「不接受分享」：这是可解释的结果，不是失败（服务端 409 recipient_refuses_shares）。
      if (e instanceof ApiClientError && e.code === 'recipient_refuses_shares') error = 'deck.sharing.refused';
      else if (e instanceof ApiClientError && e.isForbidden) error = 'deck.sharing.forbidden';
      else error = 'deck.sharing.failed';
    }
    finally { saving = false; }
  }
  async function grant(event: SubmitEvent): Promise<void> { event.preventDefault(); await change('POST', `${apiPath}/grants`, { username: username.trim(), role }); if (!error) username = ''; }
  async function createLink(event: SubmitEvent): Promise<void> { event.preventDefault(); await change('POST', `${apiPath}/links`, { password, expires_at: expiresAt }, true); password = ''; expiresAt = ''; }
  onMount(() => { void load(); });
</script>

<div class={embedded ? 'space-y-5' : 'mx-auto max-w-4xl space-y-6 px-4 py-10'} data-testid="deck-sharing-view">
  {#if loading}<p class="text-sm text-zinc-500 dark:text-zinc-400" role="status">{$t('common.loading')}</p>
  {:else if failed}<p class="text-sm text-rose-600 dark:text-rose-400" role="alert">{$t(error)}</p>
  {:else if data}
    {#if !embedded}<header><h1 class="text-2xl font-bold tracking-tight text-zinc-900 dark:text-zinc-100">{$t('deck.sharing.title')}: {data.deck_name}</h1></header>{/if}
    {#if error}<p class="text-sm text-rose-600 dark:text-rose-400" role="alert">{$t(error)}</p>{/if}
    {#if newLink}<section class="rounded-xl border border-emerald-300 bg-emerald-50 p-4 dark:border-emerald-800/80 dark:bg-emerald-950/30" role="status"><p class="text-sm">{$t('deck.sharing.link_once')}</p><a class="break-all underline" href={newLink}>{newLink}</a><button class="ml-3 underline cursor-pointer" onclick={() => newLink=''}>{$t('deck.sharing.dismiss')}</button></section>{/if}
    <section class="card-elevated space-y-4 rounded-2xl p-5"><h2 class="text-lg font-semibold text-zinc-900 dark:text-zinc-100">{$t('deck.sharing.grants')}</h2>
      <ul class="divide-y divide-zinc-200 dark:divide-zinc-800"><li class="flex items-center justify-between gap-3 py-3"><span class="text-zinc-700 dark:text-zinc-300">{data.deck_name}</span><span class="text-xs text-zinc-500 dark:text-zinc-400">{$t('deck.sharing.owner')}</span></li>{#each data.grants as grant (grant.user_id)}<li class="flex flex-wrap items-center justify-between gap-3 py-3"><span class="text-zinc-700 dark:text-zinc-300">{grant.username} · {$t(`deck.sharing.role.${grant.role}`)}</span><div class="flex gap-2"><Select class="w-28" size="sm" value={grant.role} onValueChange={(role) => change('POST', `${apiPath}/grants`, { user_id: grant.user_id, role })} ariaLabel={$t('deck.sharing.role_label')} options={[{ value: 'reader', label: $t('deck.sharing.role.reader') }, { value: 'editor', label: $t('deck.sharing.role.editor') }]} /><button class="text-rose-700 dark:text-rose-400 underline cursor-pointer" disabled={saving} onclick={() => change('DELETE',`${apiPath}/grants/${grant.user_id}`)}>{$t('deck.sharing.revoke')}</button></div></li>{/each}</ul>
      {#if (data.pending_invites ?? []).length > 0}
        <!-- 待接受：还不是成员。取消走同一个撤销端点，语义是「收回邀请」。 -->
        <div class="rounded-xl border border-amber-200/80 bg-amber-50/60 p-3 dark:border-amber-900/50 dark:bg-amber-950/20" data-testid="sharing-pending-invites">
          <p class="text-sm font-semibold text-zinc-900 dark:text-zinc-100">{$t('deck.sharing.pending_title')}</p>
          <p class="mb-2 text-xs text-zinc-500 dark:text-zinc-400">{$t('deck.sharing.pending_hint')}</p>
          <ul class="space-y-1.5">
            {#each data.pending_invites ?? [] as invite (invite.user_id)}
              <li class="flex flex-wrap items-center justify-between gap-2">
                <span class="text-sm text-zinc-700 dark:text-zinc-300">{invite.username} · {$t('deck.sharing.invited_role', { role: $t(`deck.sharing.role.${invite.role}`) })}</span>
                <button class="text-rose-700 dark:text-rose-400 underline cursor-pointer disabled:opacity-40" disabled={saving} onclick={() => change('DELETE', `${apiPath}/grants/${invite.user_id}`)}>{$t('deck.sharing.revoke')}</button>
              </li>
            {/each}
          </ul>
        </div>
      {/if}
      <form class="flex flex-wrap items-end gap-3" onsubmit={grant}><label class="grid gap-1 text-sm text-zinc-700 dark:text-zinc-300">{$t('deck.sharing.username')}<input required bind:value={username} class="field-input text-sm w-full" /></label><label class="grid gap-1 text-sm text-zinc-700 dark:text-zinc-300">{$t('deck.sharing.role_label')}<Select class="w-32" value={role} onValueChange={(value) => (role = value as typeof role)} options={[{ value: 'reader', label: $t('deck.sharing.role.reader') }, { value: 'editor', label: $t('deck.sharing.role.editor') }]} /></label><Button type="submit" disabled={saving} testId="sharing-grant">{$t('deck.sharing.grant')}</Button></form>
    </section>
    <section class="card-elevated space-y-4 rounded-2xl p-5"><h2 class="text-lg font-semibold text-zinc-900 dark:text-zinc-100">{$t('deck.sharing.visibility')}</h2><label class="grid max-w-sm gap-1 text-sm text-zinc-700 dark:text-zinc-300">{$t('deck.sharing.visibility_label')}<Select class="w-40" bind:value={visibility} options={[{ value: 'private', label: $t('deck.sharing.visibility.private') }, { value: 'unlisted', label: $t('deck.sharing.visibility.unlisted') }, { value: 'public', label: $t('deck.sharing.visibility.public') }]} /></label><Button variant="outline" disabled={saving} testId="sharing-save-visibility" onclick={() => change('PATCH',`${apiPath}/visibility`,{visibility})}>{$t('deck.sharing.save')}</Button></section>
    <section class="card-elevated space-y-4 rounded-2xl p-5"><h2 class="text-lg font-semibold text-zinc-900 dark:text-zinc-100">{$t('deck.sharing.links')}</h2><form class="flex flex-wrap items-end gap-3" onsubmit={createLink}><label class="grid gap-1 text-sm text-zinc-700 dark:text-zinc-300">{$t('deck.sharing.password')}<input type="password" bind:value={password} class="field-input text-sm w-full" /></label><label class="grid gap-1 text-sm text-zinc-700 dark:text-zinc-300">{$t('deck.sharing.expiry')}<input type="date" bind:value={expiresAt} class="field-input text-sm w-full" /></label><Button type="submit" disabled={saving} testId="sharing-create-link">{$t('deck.sharing.create_link')}</Button></form><ul class="divide-y divide-zinc-200 dark:divide-zinc-800">{#each data.links as link (link.prefix)}<li class="flex flex-wrap items-center justify-between gap-3 py-3"><span class="font-mono text-sm text-zinc-600 dark:text-zinc-300">{link.prefix} · {$t(link.revoked ? 'deck.sharing.link_revoked' : link.expired ? 'deck.sharing.link_expired' : 'deck.sharing.link_active')}{#if link.has_password} · {$t('deck.sharing.password_set')}{/if}</span>{#if !link.revoked && !link.expired}<button class="text-rose-700 dark:text-rose-400 underline cursor-pointer" disabled={saving} onclick={() => change('DELETE',`${apiPath}/links/revoke/${link.prefix}`)}>{$t('deck.sharing.revoke')}</button>{/if}</li>{/each}</ul><button disabled={saving || !data.links.some((link) => !link.revoked && !link.expired)} class="text-rose-700 dark:text-rose-400 underline disabled:opacity-40 cursor-pointer" onclick={() => change('DELETE',`${apiPath}/links`)}>{$t('deck.sharing.revoke_all')}</button></section>
  {/if}
</div>
