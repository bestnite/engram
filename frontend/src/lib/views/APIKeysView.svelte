<script lang="ts">
  import Skeleton from '../components/ui/Skeleton.svelte';
  import Page from '../components/ui/Page.svelte';
  import PageHeader from '../components/ui/PageHeader.svelte';
  import SettingsSection from '../components/ui/SettingsSection.svelte';
  import { listClasses } from '../components/ui/variants';
  import { toast } from '../components/ui/toast';
  import { Copy, X } from '@lucide/svelte';
  import { onMount } from 'svelte';
  import { t } from '../i18n';
  import { apiClient, getApiErrorMessageKey, type APIKeyRecord } from '../api';
  import Checkbox from '../components/ui/Checkbox.svelte';
  import Button from '../components/ui/Button.svelte';
  import Badge from '../components/ui/Badge.svelte';

  let keys = $state<APIKeyRecord[]>([]);
  let name = $state('');
  let scopes = $state<string[]>(['read']);
  let plaintext = $state<string | null>(null);
  let loading = $state(true);
  let saving = $state(false);
  let error = $state<string | null>(null);
  let revokingId = $state<string | null>(null);

  // 服务端返回的列表**包含已撤销的 key**（带 revoked_at）。界面按状态分两组渲染，
  // 而不是把已撤销项从本地数组里删掉——本地删除只在本会话有效，刷新后它又会回来。
  const activeKeys = $derived(keys.filter((k) => !k.revoked_at));
  const revokedKeys = $derived(keys.filter((k) => k.revoked_at));

  /** silent 为真时只换数据、不置 loading，用于撤销后的原地刷新（避免整块列表闪一下）。 */
  async function load(silent = false): Promise<void> {
    if (!silent) loading = true;
    error = null;
    try {
      keys = (await apiClient.getAPIKeys()).keys;
    } catch (err) {
      error = getApiErrorMessageKey(err);
    } finally {
      if (!silent) loading = false;
    }
  }

  async function create(event: SubmitEvent): Promise<void> {
    event.preventDefault();
    saving = true;
    error = null;
    plaintext = null;
    try {
      const result = await apiClient.createAPIKey({ name: name.trim(), scopes });
      keys = [...keys, result.key];
      plaintext = result.plaintext;
      name = '';
      scopes = ['read'];
    } catch (err) {
      error = getApiErrorMessageKey(err);
    } finally {
      saving = false;
    }
  }

  async function copyKey(): Promise<void> {
    if (!plaintext) return;
    try {
      await navigator.clipboard.writeText(plaintext);
      toast.success($t('keys.copied'));
    } catch {
      toast.error($t('notes.copy_failed'));
    }
  }

  async function revoke(key: APIKeyRecord): Promise<void> {
    if (!confirm($t('keys.confirm_revoke', { name: key.name }))) return;
    error = null;
    revokingId = key.id;
    try {
      await apiClient.deleteAPIKey(key.id);
      // 重新取数，让「已撤销」这一事实来自服务端，而不是只改本地数组。
      await load(true);
    } catch (err) {
      error = getApiErrorMessageKey(err);
    } finally {
      revokingId = null;
    }
  }

  onMount(() => load());
</script>

<Page>
  <PageHeader title={$t('keys.title')} description={$t('keys.notice')} back={{ href: '/settings', label: $t('nav.settings') }} />

  {#if error}<p class="mb-4 text-sm text-destructive-foreground" role="alert">{$t(error)}</p>{/if}

  <SettingsSection title={$t('keys.create')} description={$t('keys.create_hint')}>
    <form onsubmit={create} class="max-w-xl space-y-4">
      <label class="block text-sm font-medium text-foreground">{$t('keys.name')}
        <input required maxlength="100" bind:value={name} class="field-input mt-1.5 block w-full text-sm font-normal" />
      </label>
      <fieldset>
        <legend class="block text-sm font-medium text-foreground">{$t('keys.scopes')}</legend>
        <div class="mt-2 flex flex-wrap gap-x-5 gap-y-2 text-sm">
          {#each ['read', 'write', 'review', 'keys'] as scope}
            <label class="flex cursor-pointer items-center gap-2">
              <Checkbox
                checked={scopes.includes(scope)}
                onCheckedChange={(checked) =>
                  (scopes = checked ? [...scopes, scope] : scopes.filter((item) => item !== scope))}
                label={scope}
              />
              <span class="font-mono text-[13px]">{scope}</span>
            </label>
          {/each}
        </div>
      </fieldset>
      <Button type="submit" size="lg" testId="keys-create" disabled={saving}>{$t(saving ? 'keys.creating' : 'keys.create')}</Button>
    </form>

    {#if plaintext}
      <!-- 新密钥只显示这一次：放在表单正下方，带复制按钮。 -->
      <div class="mt-5 max-w-xl rounded-lg border border-warning/40 bg-warning/5 p-3 animate-in fade-in-0 slide-in-from-top-1 duration-200" role="status">
        <p class="text-xs text-muted-foreground">{$t('keys.created_once')}</p>
        <div class="mt-2 flex items-center gap-2">
          <input readonly value={plaintext} class="field-input min-w-0 flex-1 font-mono text-xs" aria-label={$t('keys.title')} />
          <Button variant="outline" size="lg" onclick={copyKey}><Copy class="size-4" aria-hidden="true" />{$t('deck.sharing.copy')}</Button>
          <Button variant="ghost" size="icon" label={$t('keys.dismiss')} onclick={() => (plaintext = null)}><X class="size-4" aria-hidden="true" /></Button>
        </div>
      </div>
    {/if}
  </SettingsSection>

  <SettingsSection title={$t('keys.list_heading')}>
    {#if loading}
      <Skeleton testId="keys-loading" label={$t('common.loading')} lines={2} />
    {:else if keys.length === 0}
      <p class="text-sm text-muted-foreground">{$t('keys.empty')}</p>
    {:else}
      <ul class={listClasses.root}>
        {#each activeKeys as key (key.id)}
          <li class="flex items-center gap-3 border-b border-border px-4 py-2.5 last:border-b-0">
            <div class="min-w-0 flex-1">
              <p class="truncate text-sm font-medium text-foreground">{key.name}</p>
              <p class="truncate font-mono text-xs text-muted-foreground">{key.prefix} · {key.scopes}</p>
            </div>
            <Button
              variant="ghost"
              size="sm"
              class="hover:bg-destructive-soft hover:text-destructive-foreground"
              testId="keys-revoke-{key.id}"
              disabled={revokingId === key.id}
              onclick={() => revoke(key)}
            >{$t(revokingId === key.id ? 'keys.revoking' : 'keys.revoke')}</Button>
          </li>
        {/each}
        {#each revokedKeys as key (key.id)}
          <li class="flex items-center gap-3 border-b border-border px-4 py-2.5 last:border-b-0">
            <div class="min-w-0 flex-1">
              <p class="truncate text-sm text-muted-foreground line-through">{key.name}</p>
              <p class="truncate font-mono text-xs text-muted-foreground">{key.prefix} · {key.scopes}</p>
            </div>
            <Badge testId={`keys-revoked-badge-${key.id}`}>{$t('keys.revoked')}</Badge>
          </li>
        {/each}
      </ul>
    {/if}
  </SettingsSection>
</Page>
