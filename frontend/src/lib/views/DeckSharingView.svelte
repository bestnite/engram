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
  import { Copy, KeyRound, UserMinus, X } from '@lucide/svelte';

  // embedded 为真时本组件嵌在卡组详情的「共享」Tab 内：不渲染页面级宽度/内边距外壳，
  // 也不渲染页头标题（Tab 已表明当前区块）。直接访问 /decks/:id/sharing 时为假，独立成页。
  interface Props {
    embedded?: boolean;
  }
  let { embedded = false }: Props = $props();

  type Grant = { user_id: string; username: string; role: 'reader' | 'editor' };
  type Link = { prefix: string; created_at: string; expires_at: string | null; has_password: boolean; revoked: boolean; expired: boolean };
  // 待接受的邀请：属主必须能区分「已授权」与「邀请了还没答应」——后者随时可能被拒绝，
  // 界面上不该显示成已有访问权（同意制）。
  type Invite = { user_id: string; username: string; role: 'reader' | 'editor'; expires_at: string };
  type Sharing = { deck_id: string; deck_name: string; grants: Grant[]; pending_invites?: Invite[]; links: Link[] };
  let data = $state<Sharing | null>(null);
  let loading = $state(true);
  let failed = $state(false);
  let saving = $state(false);
  // 哪个表单的提交在途：只有它的按钮转圈；saving 仍让其余写操作一并禁用。
  let submitting = $state<'grant' | 'link' | null>(null);
  let error = $state('');
  let username = $state('');
  let role = $state<'reader'|'editor'>('reader');
  let password = $state('');
  let expiresAt = $state('');
  let newLink = $state('');
  const deckId = $derived($routeStore.params.id || '');
  const apiPath = $derived(`/api/v1/decks/${encodeURIComponent(deckId)}/sharing`);
  async function load(): Promise<void> {
    loading = true; failed = false;
    try { data = await apiClient.request<Sharing>(apiPath); }
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
      toast.error($t(error));
    }
    finally { saving = false; }
  }
  async function grant(event: SubmitEvent): Promise<void> { event.preventDefault(); submitting = 'grant'; await change('POST', `${apiPath}/grants`, { username: username.trim(), role }); submitting = null; if (!error) username = ''; }
  async function createLink(event: SubmitEvent): Promise<void> { event.preventDefault(); submitting = 'link'; await change('POST', `${apiPath}/links`, { password, expires_at: expiresAt }, true); submitting = null; password = ''; expiresAt = ''; }
  async function copyLink(): Promise<void> {
    try { await navigator.clipboard.writeText(newLink); toast.success($t('deck.sharing.link_copied')); }
    catch { toast.error($t('notes.copy_failed')); }
  }
  const roleOptions = $derived([{ value: 'reader', label: $t('deck.sharing.role.reader') }, { value: 'editor', label: $t('deck.sharing.role.editor') }]);
  const activeLinks = $derived(data ? data.links.filter((link) => !link.revoked && !link.expired).length : 0);
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
          <!-- 待接受：还不是成员，对方接受之后才有访问权（同意制）。取消走同一个撤销端点。 -->
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

    <SettingsSection title={$t('deck.sharing.links')} description={$t('deck.sharing.links_hint')} testId="sharing-links">
      <form class="flex flex-wrap items-end gap-2" onsubmit={createLink}>
        <label class="min-w-40 flex-1 text-sm font-medium text-foreground">{$t('deck.sharing.password')}
          <input type="password" bind:value={password} class="field-input mt-1.5 block w-full text-sm" />
        </label>
        <label class="text-sm font-medium text-foreground">{$t('deck.sharing.expiry')}
          <input type="date" bind:value={expiresAt} class="field-input mt-1.5 block w-full text-sm" />
        </label>
        <Button type="submit" size="lg" variant="outline" loading={submitting === 'link'} disabled={saving} testId="sharing-create-link">{$t('deck.sharing.create_link')}</Button>
      </form>

      {#if newLink}
        <!-- 新链接只显示这一次：放在表单正下方，带复制按钮。 -->
        <div class="mt-4 rounded-lg border border-success/40 bg-success/5 p-3 animate-in fade-in-0 slide-in-from-top-1 duration-200" role="status">
          <p class="text-xs text-muted-foreground">{$t('deck.sharing.link_once')}</p>
          <div class="mt-2 flex items-center gap-2">
            <input readonly value={newLink} class="field-input min-w-0 flex-1 font-mono text-xs" aria-label={$t('deck.sharing.links')} />
            <Button variant="outline" size="lg" onclick={copyLink}><Copy class="size-4" aria-hidden="true" />{$t('deck.sharing.copy')}</Button>
            <Button variant="ghost" size="icon" label={$t('deck.sharing.dismiss')} onclick={() => (newLink = '')}><X class="size-4" aria-hidden="true" /></Button>
          </div>
        </div>
      {/if}

      {#if data.links.length > 0}
        <ul class="{listClasses.root} mt-4">
          {#each data.links as link (link.prefix)}
            {@const live = !link.revoked && !link.expired}
            <li class="flex items-center gap-3 border-b border-border px-4 py-2.5 last:border-b-0">
              <span class="min-w-0 flex-1 truncate font-mono text-[13px] {live ? 'text-foreground' : 'text-muted-foreground'}">{link.prefix}</span>
              {#if link.has_password}
                <span class="inline-flex items-center gap-1 text-xs text-muted-foreground" title={$t('deck.sharing.password_set')}><KeyRound class="size-3.5" aria-hidden="true" />{$t('deck.sharing.password_set')}</span>
              {/if}
              <Badge variant={live ? 'success' : 'neutral'}>{$t(link.revoked ? 'deck.sharing.link_revoked' : link.expired ? 'deck.sharing.link_expired' : 'deck.sharing.link_active')}</Badge>
              {#if live}
                <Button variant="ghost" size="sm" class="hover:bg-destructive-soft hover:text-destructive-foreground" disabled={saving} onclick={() => change('DELETE', `${apiPath}/links/revoke/${link.prefix}`)}>{$t('deck.sharing.revoke')}</Button>
              {/if}
            </li>
          {/each}
        </ul>
        {#if activeLinks > 1}
          <div class="mt-3 flex justify-end">
            <Button variant="danger-outline" size="sm" disabled={saving} onclick={() => change('DELETE', `${apiPath}/links`)}>{$t('deck.sharing.revoke_all')}</Button>
          </div>
        {/if}
      {/if}
    </SettingsSection>
  {/if}
</Page>
