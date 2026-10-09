<script lang="ts">
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

<div class="py-10 max-w-4xl mx-auto px-4">
  <div class="card-elevated p-6 sm:p-8 rounded-2xl space-y-6">
    <header>
      <h1 class="text-2xl font-bold text-zinc-900 dark:text-zinc-100">{$t('keys.title')}</h1>
      <p class="mt-1 text-sm text-zinc-600 dark:text-zinc-400">{$t('keys.notice')}</p>
    </header>

    {#if error}<p class="text-sm text-rose-600 dark:text-rose-400" role="alert">{$t(error)}</p>{/if}

    {#if plaintext}
      <section class="rounded-xl border border-amber-300 bg-amber-50 p-4 text-sm dark:border-amber-800/80 dark:bg-amber-950/30" role="status">
        <p>{$t('keys.created_once')}</p>
        <code class="mt-2 block break-all select-all">{plaintext}</code>
        <button class="mt-2 underline cursor-pointer" type="button" onclick={() => (plaintext = null)}>{$t('keys.dismiss')}</button>
      </section>
    {/if}

    <form onsubmit={create} class="rounded-xl border border-zinc-200 dark:border-zinc-700/80 p-5 space-y-4">
      <label class="block text-sm">{$t('keys.name')}<input required maxlength="100" bind:value={name} class="field-input text-sm mt-1 w-full" /></label>
      <fieldset class="flex flex-wrap gap-4 text-sm"><legend class="mb-2">{$t('keys.scopes')}</legend>
        {#each ['read', 'write', 'review', 'keys'] as scope}
          <label class="flex cursor-pointer items-center gap-1.5">
            <Checkbox
              checked={scopes.includes(scope)}
              onCheckedChange={(checked) =>
                (scopes = checked ? [...scopes, scope] : scopes.filter((item) => item !== scope))}
              label={scope}
            />
            <span>{scope}</span>
          </label>
        {/each}
      </fieldset>
      <Button type="submit" testId="keys-create" disabled={saving}>{$t(saving ? 'keys.creating' : 'keys.create')}</Button>
    </form>

    {#if loading}
      <p class="text-sm text-zinc-500 dark:text-zinc-400">{$t('common.loading')}</p>
    {:else if keys.length === 0}
      <p class="text-sm text-zinc-500 dark:text-zinc-400">{$t('keys.empty')}</p>
    {:else}
      <ul class="divide-y divide-zinc-200 dark:divide-zinc-800">
        {#each activeKeys as key (key.id)}
          <li class="flex items-center justify-between gap-4 py-3">
            <div>
              <strong class="text-zinc-900 dark:text-zinc-100">{key.name}</strong>
              <p class="text-xs text-zinc-500 dark:text-zinc-400">{key.prefix} · {key.scopes}</p>
            </div>
            <button
              type="button"
              data-testid="keys-revoke-{key.id}"
              disabled={revokingId === key.id}
              class="text-sm text-rose-600 dark:text-rose-400 underline disabled:opacity-50 cursor-pointer"
              onclick={() => revoke(key)}
            >{$t(revokingId === key.id ? 'keys.revoking' : 'keys.revoke')}</button>
          </li>
        {/each}
        {#each revokedKeys as key (key.id)}
          <li class="flex items-center justify-between gap-4 py-3 opacity-70">
            <div>
              <strong class="text-zinc-600 dark:text-zinc-300 line-through">{key.name}</strong>
              <p class="text-xs text-zinc-500 dark:text-zinc-400">{key.prefix} · {key.scopes}</p>
            </div>
            <Badge testId={`keys-revoked-badge-${key.id}`}>{$t('keys.revoked')}</Badge>
          </li>
        {/each}
      </ul>
    {/if}
  </div>
</div>
