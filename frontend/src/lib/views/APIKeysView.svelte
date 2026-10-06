<script lang="ts">
  import { onMount } from 'svelte';
  import { t } from '../i18n';
  import { apiClient, getApiErrorMessageKey, type APIKeyRecord } from '../api';

  let keys = $state<APIKeyRecord[]>([]);
  let name = $state('');
  let scopes = $state<string[]>(['read']);
  let plaintext = $state<string | null>(null);
  let loading = $state(true);
  let saving = $state(false);
  let error = $state<string | null>(null);

  async function load(): Promise<void> {
    loading = true;
    error = null;
    try {
      keys = (await apiClient.getAPIKeys()).keys;
    } catch (err) {
      error = getApiErrorMessageKey(err);
    } finally {
      loading = false;
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
    try {
      await apiClient.deleteAPIKey(key.id);
      keys = keys.filter((item) => item.id !== key.id);
    } catch (err) {
      error = getApiErrorMessageKey(err);
    }
  }

  onMount(load);
</script>

<div class="py-10 max-w-4xl mx-auto px-4">
  <div class="card-elevated p-8 rounded-xl space-y-6">
    <header>
      <h1 class="text-2xl font-bold text-zinc-900 dark:text-zinc-100">{$t('keys.title')}</h1>
      <p class="mt-1 text-sm text-zinc-600 dark:text-zinc-400">{$t('keys.notice')}</p>
    </header>
    {#if error}<p class="text-sm text-rose-600" role="alert">{$t(error)}</p>{/if}
    {#if plaintext}
      <section class="rounded-xl border border-amber-300 bg-amber-50 p-4 text-sm dark:bg-amber-950/30" role="status">
        <p>{$t('keys.created_once')}</p>
        <code class="mt-2 block break-all select-all">{plaintext}</code>
        <button class="mt-2 underline" type="button" onclick={() => (plaintext = null)}>{$t('keys.dismiss')}</button>
      </section>
    {/if}
    <form onsubmit={create} class="card-subtle p-5 rounded-xl space-y-4">
      <label class="block text-sm">{$t('keys.name')}<input required maxlength="100" bind:value={name} class="mt-1 w-full rounded-lg border p-2 dark:bg-zinc-900" /></label>
      <fieldset class="flex flex-wrap gap-4 text-sm"><legend class="mb-2">{$t('keys.scopes')}</legend>
        {#each ['read', 'write', 'review', 'keys'] as scope}
          <label><input type="checkbox" value={scope} bind:group={scopes} /> {scope}</label>
        {/each}
      </fieldset>
      <button disabled={saving} class="rounded-lg bg-zinc-950 px-4 py-2 text-white disabled:opacity-50">{$t(saving ? 'keys.creating' : 'keys.create')}</button>
    </form>
    {#if loading}<p>{$t('common.loading')}</p>{:else if keys.length === 0}<p>{$t('keys.empty')}</p>{:else}
      <ul class="divide-y divide-zinc-200 dark:divide-zinc-800">
        {#each keys as key (key.id)}
          <li class="flex items-center justify-between gap-4 py-3">
            <div><strong>{key.name}</strong><p class="text-xs text-zinc-500">{key.prefix} · {key.scopes}</p></div>
            <button type="button" class="text-sm text-rose-600 underline" onclick={() => revoke(key)}>{$t('keys.revoke')}</button>
          </li>
        {/each}
      </ul>
    {/if}
  </div>
</div>
