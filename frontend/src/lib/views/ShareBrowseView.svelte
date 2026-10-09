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

  // 入伙状态：joining 期间禁用按钮；joined 后换成成功提示与「打开卡组」。
  let joining = $state(false);
  let joined = $state(false);
  let joinedDeckId = $state<number | null>(null);
  let joinErrorKey = $state<string | null>(null);

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
    errorKey = null;
    try {
      const res = await apiClient.unlockShare(token, password);
      share = res;
      status = 'content';
    } catch (err) {
      errorKey = mapShareError(err);
    } finally {
      unlocking = false;
    }
  }

  // 入伙：把链接指的卡组加进自己的列表。链接失效/被撤销时服务端返回 404，按同一套 share.* 文案提示。
  async function join(): Promise<void> {
    joining = true;
    joinErrorKey = null;
    try {
      const res = await apiClient.joinSharedDeck(token);
      joined = true;
      joinedDeckId = res.deck_id;
    } catch (err) {
      joinErrorKey = err instanceof ApiClientError && err.isNotFound
        ? 'share.error_not_found'
        : err instanceof ApiClientError && err.isNetworkError
          ? 'error.network'
          : 'share.browse.join_failed';
    } finally {
      joining = false;
    }
  }

  onMount(loadShare);
</script>

<Page>
  <div class="mb-6">
    <h1 class="text-2xl font-semibold tracking-tight text-foreground">
      {$t('share.browse.heading')}
    </h1>
    {#if share}
      <p data-testid="share-deck-name" class="mt-1 text-sm text-muted-foreground">{share.deck_name}</p>
    {/if}
  </div>

  {#if status === 'loading'}
    <Skeleton testId="share-loading" label={$t('share.browse.loading')} lines={2} />
  {:else if status === 'error'}
    <div
      data-testid="share-error"
      class="p-4 rounded-lg bg-rose-50 dark:bg-rose-950/40 border border-rose-200 dark:border-rose-800/60 text-rose-700 dark:text-rose-300 text-sm"
    >
      <span>{errorKey ? $t(errorKey) : $t('share.error_not_found')}</span>
    </div>
  {:else if status === 'password'}
    <div class="card-elevated p-6 rounded-xl max-w-md">
      <p class="mb-4 text-sm text-muted-foreground">{$t('share.password.intro')}</p>
      <form onsubmit={handleUnlock} class="space-y-4">
        <div>
          <label for="share-password" class="block text-sm font-medium text-foreground/80 mb-1.5">
            {$t('share.password.label')}
          </label>
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
            class="field-input text-sm w-full transition-colors disabled:opacity-50"
          />
        </div>
        {#if errorKey}
          <p data-testid="share-password-error" class="text-sm text-rose-600 dark:text-rose-400">{$t(errorKey)}</p>
        {/if}
        <Button type="submit" disabled={unlocking || !password} variant="primary" size="lg" class="w-full" testId="share-browse-submit">
          {unlocking ? $t('share.password.submitting') : $t('share.password.submit')}
        </Button>
      </form>
    </div>
  {:else if share}
    <p class="mb-6 text-sm text-muted-foreground">{$t('share.browse.intro')}</p>

    {#if share.notes.length === 0}
      <p data-testid="share-empty" class="text-sm text-muted-foreground">{$t('share.browse.empty')}</p>
    {:else}
      <ul class="space-y-4">
        {#each share.notes as note, i}
          <li data-testid="share-note" class="card-elevated p-5 rounded-xl">
            <div class="text-xs font-semibold uppercase tracking-wider text-muted-foreground/70">{$t('share.browse.front')}</div>
            <div class="mt-2 text-base text-foreground leading-relaxed">{@html note.front_html}</div>
            <hr class="my-4 border-border" />
            <div class="text-xs font-semibold uppercase tracking-wider text-muted-foreground/70">{$t('share.browse.back')}</div>
            <div class="mt-2 text-base text-foreground/80 leading-relaxed">{@html note.back_html}</div>
            <span class="sr-only">{i + 1}</span>
          </li>
        {/each}
      </ul>
    {/if}

    <div class="mt-8 text-center text-sm">
      {#if $authStore.authenticated}
        {#if joined}
          <p data-testid="share-joined" class="text-emerald-700 dark:text-emerald-400">{$t('share.browse.joined')}</p>
          {#if joinedDeckId !== null}
            <a
              data-testid="share-open-deck"
              href={`/decks/${joinedDeckId}`}
              class="mt-2 inline-block text-muted-foreground hover:text-foreground transition-colors"
            >
              {$t('share.browse.open_deck')}
            </a>
          {/if}
        {:else}
          <Button variant="primary" disabled={joining} onclick={join} testId="share-join">
            {joining ? $t('share.browse.joining') : $t('share.browse.join')}
          </Button>
          {#if joinErrorKey}
            <p data-testid="share-join-error" class="mt-2 text-rose-600 dark:text-rose-400">{$t(joinErrorKey)}</p>
          {/if}
        {/if}
      {:else}
        <a href="/login" class="text-muted-foreground hover:text-foreground transition-colors">
          {$t('share.browse.login')}
        </a>
      {/if}
    </div>
  {/if}
</Page>
