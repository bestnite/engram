<script lang="ts">
  import SettingsNav from './SettingsNav.svelte';
  import { askConfirm } from '../components/ui/confirm';
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
  let revokingId = $state<string | null>(null);

  // 服务端返回的列表**包含已撤销的 key**（带 revoked_at）。界面按状态分两组渲染，
  // 而不是把已撤销项从本地数组里删掉——本地删除只在本会话有效，刷新后它又会回来。
  const activeKeys = $derived(keys.filter((k) => !k.revoked_at));
  const revokedKeys = $derived(keys.filter((k) => k.revoked_at));

  /** silent 为真时只换数据、不置 loading，用于撤销后的原地刷新（避免整块列表闪一下）。 */
  async function load(silent = false): Promise<void> {
    if (!silent) loading = true;
    try {
      keys = (await apiClient.getAPIKeys()).keys;
    } catch (err) {
      toast.error($t(getApiErrorMessageKey(err)));
    } finally {
      if (!silent) loading = false;
    }
  }

  async function create(event: SubmitEvent): Promise<void> {
    event.preventDefault();
    saving = true;
    plaintext = null;
    try {
      const result = await apiClient.createAPIKey({ name: name.trim(), scopes });
      keys = [...keys, result.key];
      plaintext = result.plaintext;
      name = '';
      scopes = ['read'];
    } catch (err) {
      toast.error($t(getApiErrorMessageKey(err)));
    } finally {
      saving = false;
    }
  }

  async function copyText(text: string, okKey: string): Promise<void> {
    try {
      await navigator.clipboard.writeText(text);
      toast.success($t(okKey));
    } catch {
      toast.error($t('notes.copy_failed'));
    }
  }

  async function copyKey(): Promise<void> {
    if (plaintext) await copyText(plaintext, 'keys.copied');
  }

  // 接入地址取浏览器当前访问的站点地址：经过反向代理时，这正是外部客户端能访问到的地址，
  // 不依赖服务端的 BASE_URL 配置是否正确。服务端渲染（测试）时没有 window，地址留空。
  const origin = typeof window !== 'undefined' && window.location ? window.location.origin : '';
  const restUrl = `${origin}/api/v1`;
  const mcpUrl = `${origin}/mcp`;

  // MCP 客户端配置片段：格式与 README 一致。刚创建的密钥只在这一刻可见，此时直接填进片段，
  // 其余时候用占位符，提醒用户换成自己保存的密钥。
  const mcpConfig = $derived(
    JSON.stringify(
      { mcpServers: { engram: { url: mcpUrl, headers: { Authorization: `Bearer ${plaintext ?? 'YOUR_API_KEY'}` } } } },
      null,
      2
    )
  );

  const scopeOptions = ['read', 'write', 'review', 'keys'];

  async function revoke(key: APIKeyRecord): Promise<void> {
    if (!(await askConfirm({ title: $t('keys.confirm_revoke', { name: key.name }), confirmLabel: $t('keys.revoke'), destructive: true }))) return;
    revokingId = key.id;
    try {
      await apiClient.deleteAPIKey(key.id);
      // 重新取数，让「已撤销」这一事实来自服务端，而不是只改本地数组。
      await load(true);
    } catch (err) {
      toast.error($t(getApiErrorMessageKey(err)));
    } finally {
      revokingId = null;
    }
  }

  onMount(() => load());
</script>

<Page>
  <SettingsNav />
  <PageHeader title={$t('settings.api.heading')} testId="api-title" description={$t('settings.api.intro')} />

  <SettingsSection title={$t('settings.api.endpoints.heading')} description={$t('settings.api.endpoints.hint')} testId="api-endpoints">
    <dl class="max-w-2xl space-y-4">
      {#each [
        { label: 'settings.api.endpoints.mcp', value: mcpUrl, testId: 'api-mcp-url', note: 'settings.api.endpoints.mcp_note' },
        { label: 'settings.api.endpoints.rest', value: restUrl, testId: 'api-rest-url', note: 'settings.api.endpoints.rest_note' },
      ] as row (row.testId)}
        <div>
          <dt class="text-sm font-medium text-foreground">{$t(row.label)}</dt>
          <dd class="mt-1.5 flex items-center gap-2">
            <code data-testid={row.testId} class="min-w-0 flex-1 select-all truncate rounded-md bg-surface px-3 py-2 font-mono text-[13px] text-foreground">{row.value}</code>
            <Button variant="outline" size="icon" class="size-9" label={$t('deck.sharing.copy')} title={$t('deck.sharing.copy')} onclick={() => copyText(row.value, 'settings.api.copied')}>
              <Copy class="size-4" aria-hidden="true" />
            </Button>
          </dd>
          <p class="mt-1 text-xs text-muted-foreground">{$t(row.note)}</p>
        </div>
      {/each}
    </dl>
  </SettingsSection>

  <SettingsSection title={$t('settings.api.client.heading')} description={$t('settings.api.client.hint')} testId="api-client-config">
    <div class="relative max-w-2xl">
      <pre data-testid="api-mcp-config" class="overflow-x-auto rounded-lg border border-border bg-surface p-4 pr-14 font-mono text-[13px] leading-relaxed text-foreground">{mcpConfig}</pre>
      <Button variant="ghost" size="icon" class="absolute right-2 top-2" label={$t('deck.sharing.copy')} title={$t('deck.sharing.copy')} onclick={() => copyText(mcpConfig, 'settings.api.copied')}>
        <Copy class="size-4" aria-hidden="true" />
      </Button>
    </div>
    {#if plaintext}
      <p class="mt-2 text-xs text-warning">{$t('settings.api.client.key_filled')}</p>
    {/if}
  </SettingsSection>

  <SettingsSection title={$t('keys.create')} description={$t('keys.create_hint')}>
    <form onsubmit={create} class="max-w-xl space-y-4">
      <label class="block text-sm font-medium text-foreground">{$t('keys.name')}
        <input required maxlength="100" bind:value={name} class="field-input mt-1.5 block w-full text-sm font-normal" />
      </label>
      <fieldset>
        <legend class="block text-sm font-medium text-foreground">{$t('keys.scopes')}</legend>
        <!-- 每个权限后面写明它放开什么，与服务端的定义一致（scope 常量的注释）。 -->
        <div class="mt-2 space-y-2.5">
          {#each scopeOptions as scope}
            <label class="flex cursor-pointer items-start gap-2.5">
              <Checkbox
                class="mt-0.5"
                testId="keys-scope-{scope}"
                checked={scopes.includes(scope)}
                onCheckedChange={(checked) =>
                  (scopes = checked ? [...scopes, scope] : scopes.filter((item) => item !== scope))}
                label={scope}
              />
              <span class="text-sm">
                <span class="font-mono text-[13px] font-medium text-foreground">{scope}</span>
                <span class="text-muted-foreground"> — {$t('settings.api.scope.' + scope)}</span>
              </span>
            </label>
          {/each}
        </div>
      </fieldset>
      <Button type="submit" size="lg" testId="keys-create" loading={saving}>{$t('keys.create')}</Button>
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
              loading={revokingId === key.id}
              onclick={() => revoke(key)}
            >{$t('keys.revoke')}</Button>
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
