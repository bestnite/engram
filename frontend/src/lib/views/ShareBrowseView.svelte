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

  // 公开只读分享浏览页（服务端 GET /s/:token 切壳后由客户端路由渲染此页）。
  // 卡片正反面一律是服务端清洗后的 HTML，这里只把清洗结果作为标签注入，绝不把 fields 原文当 Markdown 渲染。
  // 媒体（<img src="/media/<sha>">）的可见性仍由服务端判定：只有登录且打开过该卡组的会话才放行。
  //
  // 打开链接只登记「这个会话看过它」，不会把卡组加进访客的列表——链接会被转发，
  // 随手点开一次不该等于同意接收一个卡组。入伙是下面的显式动作（「加入我的卡组」）。
  const token = $derived($routeStore.params.token ?? '');

  let status = $state<'loading' | 'password' | 'content' | 'error'>('loading');
  let share = $state<ShareResponse | null>(null);
  let password = $state('');
  let unlocking = $state(false);
  let errorKey = $state<string | null>(null);

  // 入伙状态：joining 期间按钮转圈；joined 后加入按钮换成「打开卡组」，结果走 toast。
  let joining = $state(false);
  let joined = $state(false);
  let joinedDeckId = $state<number | null>(null);

  // 把分享接口的稳定错误 code 映射到 share.* 语言包键；绝不回显后端英文 message。
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

  // 入伙：把链接指的卡组加进自己的列表。链接失效/被撤销时服务端返回 404，按同一套 share.* 文案提示。
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
      <!-- 卡片预览：一行一张卡，左正面右背面，不再一张张卡片竖着堆。 -->
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
