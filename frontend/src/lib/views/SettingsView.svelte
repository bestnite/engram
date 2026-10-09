<script lang="ts">
  import { onMount } from 'svelte';
  import { t, localeStore, setLocale, type SupportedLocale, isSupportedLocale } from '../i18n';
  import {
    apiClient,
    ApiClientError,
    getApiErrorMessageKey,
    validateProfileForm,
    DEFAULT_DAY_CUTOFF_HOUR,
    DEFAULT_LEARN_AHEAD_MINUTES,
    MAX_LEARN_AHEAD_MINUTES,
    type UserProfile,
    type ShareAllowRow,
  } from '../api';
  import { timezoneOptions } from '../timezones';
  import Select from '../components/ui/Select.svelte';
  import Combobox from '../components/ui/Combobox.svelte';
  import Skeleton from '../components/ui/Skeleton.svelte';
  import Button from '../components/ui/Button.svelte';

  interface Props {
    initialLoading?: boolean;
  }

  // 测试用它直接进入已加载态（与 NotificationPrefsView / TOTPView 同一模式）：
  // 应用挂载时不传，默认仍先渲染骨架再拉数据。
  let { initialLoading = true }: Props = $props();

  // 视图响应式状态（Svelte 5 runes）
  // svelte-ignore state_referenced_locally
  let loading = $state(initialLoading);
  let saving = $state(false);
  let serverApiAvailable = $state<boolean | null>(null);
  let savedNotice = $state<string | null>(null);
  let generalError = $state<string | null>(null);
  let passwordError = $state<string | null>(null);
  let passwordNotice = $state<string | null>(null);
  let passwordSaving = $state(false);

  // 卡组共享接收策略（同意制）：谁可以把卡组分享给我。
  // allowList 存服务端回读的真值（带用户名），本地不推断并集运算的结果。
  let sharePolicy = $state<'anyone' | 'whitelist' | 'nobody'>('anyone');
  let allowList = $state<ShareAllowRow[]>([]);
  let allowName = $state('');
  let shareSaving = $state(false);
  let shareNotice = $state<string | null>(null);
  let shareError = $state<string | null>(null);
  let oldPassword = $state('');
  let newPassword = $state('');
  let fieldErrors = $state<Partial<Record<'display_name' | 'locale' | 'timezone' | 'day_cutoff_hour' | 'learn_ahead_minutes', string>>>({});

  // 表单字段绑定
  let displayName = $state('');
  let selectedLocale = $state<SupportedLocale>($localeStore);
  let timezone = $state('');
  // 切点必须是一个具体整点：界面不提供「未设置」，库里为 NULL 的旧账号按服务端默认值显示。
  let dayCutoff = $state(String(DEFAULT_DAY_CUTOFF_HOUR));
  // 提前学习窗口（分钟）：库里为 NULL 时显示默认值，保存后成为显式值；0 表示关闭。
  let learnAhead = $state(String(DEFAULT_LEARN_AHEAD_MINUTES));

  // 候选项要跟着当前值算：库里可能存着本浏览器不认识的历史时区名，不并进列表就会显示成空。
  const timezoneChoices = $derived(timezoneOptions(timezone));
  const cutoffChoices = $derived(
    Array.from({ length: 24 }, (_, hour) => ({
      value: String(hour),
      label: `${String(hour).padStart(2, '0')}:00`,
    }))
  );

  /**
   * 初始化探测与加载已有用户数据
   * 若服务端尚未实现 JSON 端点（404 Not Found），安全优雅地切入本地状态模式，
   * 严禁无根据臆测或向 SSR 表单端点伪造请求。
   */
  async function loadProfile(): Promise<void> {
    loading = true;
    generalError = null;

    // 默认时区回退：优先尝试浏览器本地环境时区
    if (typeof Intl !== 'undefined' && Intl.DateTimeFormat) {
      try {
        timezone = Intl.DateTimeFormat().resolvedOptions().timeZone || 'UTC';
      } catch {
        timezone = 'UTC';
      }
    } else {
      timezone = 'UTC';
    }

    try {
      const profile: UserProfile = await apiClient.getProfile();
      serverApiAvailable = true;
      if (profile.display_name) {
        displayName = profile.display_name;
      }
      if (isSupportedLocale(profile.locale)) {
        selectedLocale = profile.locale;
        setLocale(profile.locale);
      }
      if (profile.timezone) {
        timezone = profile.timezone;
      }
      // 0 是合法切点（午夜）；NULL 是旧账号的未设置状态，界面按服务端默认值显示，保存后成为显式值。
      dayCutoff = String(profile.day_cutoff_hour ?? DEFAULT_DAY_CUTOFF_HOUR);
      learnAhead = String(profile.learn_ahead_minutes ?? DEFAULT_LEARN_AHEAD_MINUTES);
    } catch (err) {
      if (err instanceof ApiClientError && err.isNotFound) {
        // 服务端尚未提供 profile JSON 端点（API Gap 明确报告），安全降级并保留前端安全输入与本地生效
        serverApiAvailable = false;
      } else if (err instanceof ApiClientError && err.isUnauthorized) {
        generalError = 'error.unauthorized';
      } else {
        generalError = getApiErrorMessageKey(err);
      }
    } finally {
      loading = false;
    }
  }

  /**
   * 切换界面语言：前端 UI 即时同步更新响应式 store
   */
  function handleLocaleChange(nextLocale: string): void {
    if (isSupportedLocale(nextLocale)) {
      selectedLocale = nextLocale;
      setLocale(nextLocale);
      // 清除可能存在的语言单项校验错误
      if (fieldErrors.locale) {
        const { locale: _, ...rest } = fieldErrors;
        fieldErrors = rest;
      }
    }
  }

  // 页头切换器与这里共用同一份 store：它改完语言（已登录时直接落库），表单字段必须跟着走，
  // 否则在设置页上点保存会把旧语言原样写回去（界面显示新语言、库里回到旧语言）。
  $effect(() => {
    selectedLocale = $localeStore;
  });

  /**
   * 提交个人资料与设置
   */
  async function handleSubmit(event: Event): Promise<void> {
    event.preventDefault();
    savedNotice = null;
    generalError = null;
    fieldErrors = {};

    const rawCutoff = dayCutoff;
    const validation = validateProfileForm({
      display_name: displayName,
      locale: selectedLocale,
      timezone,
      day_cutoff_hour: rawCutoff,
      learn_ahead_minutes: learnAhead,
    });

    if (!validation.valid || !validation.data) {
      fieldErrors = validation.errors;
      return;
    }

    saving = true;

    // 若服务端确认缺失 JSON API，不向 SSR 表单虚构请求，给出清晰明确的本地生效与缺口提示
    if (serverApiAvailable === false) {
      savedNotice = 'settings.profile.api_unavailable';
      saving = false;
      return;
    }

    try {
      const updated = await apiClient.updateProfile(validation.data);
      serverApiAvailable = true;
      if (isSupportedLocale(updated.locale)) {
        selectedLocale = updated.locale;
        setLocale(updated.locale);
      }
      savedNotice = 'settings.profile.saved';
    } catch (err) {
      if (err instanceof ApiClientError && err.isNotFound) {
        serverApiAvailable = false;
        savedNotice = 'settings.profile.api_unavailable';
      } else {
        generalError = getApiErrorMessageKey(err);
      }
    } finally {
      saving = false;
    }
  }

  async function handlePasswordSubmit(event: Event): Promise<void> {
    event.preventDefault(); passwordError = null; passwordNotice = null; passwordSaving = true;
    try { await apiClient.changePassword({ old_password: oldPassword, new_password: newPassword }); oldPassword = ''; newPassword = ''; passwordNotice = 'settings.password.changed'; }
    catch (err) {
      const code = err instanceof ApiClientError ? err.code : '';
      passwordError = ({ invalid_current_password: 'settings.password.current_wrong', password_rejected: 'settings.password.rejected', password_unchanged: 'settings.password.unchanged', password_unavailable: 'settings.password.unavailable', invalid_request: 'settings.password.invalid' } as Record<string, string>)[code] || getApiErrorMessageKey(err);
    } finally { passwordSaving = false; }
  }

  /** 读取当前接收策略与白名单（GET /api/v1/settings/share-policy）。 */
  async function loadSharePolicy(): Promise<void> {
    try {
      const res = await apiClient.getShareInvites();
      sharePolicy = res.policy;
      allowList = res.allow_list ?? [];
    } catch {
      // 读不到就保持默认（anyone）：这正是「没有特殊设置」的真实含义，不是错误。
    }
  }

  /**
   * 保存接收策略（PUT /api/v1/settings/share-policy）。
   *
   * 保存成功后以服务端返回的策略与白名单为准——白名单是并集语义，本地推断会漂移。
   * 未知用户名由服务端返回 user_not_found，这里翻成对应文案。
   */
  async function saveSharePolicy(input: {
    policy?: 'anyone' | 'whitelist' | 'nobody';
    allow_usernames?: string[];
    revoke?: string[];
  }): Promise<void> {
    shareSaving = true;
    shareNotice = null;
    shareError = null;
    try {
      const res = await apiClient.saveSharePolicy(input);
      sharePolicy = res.policy;
      allowList = res.allow_list ?? [];
      allowName = '';
      shareNotice = 'settings.share_policy.saved';
    } catch (err) {
      shareError =
        err instanceof ApiClientError && err.code === 'user_not_found'
          ? 'settings.share_policy.user_not_found'
          : 'settings.share_policy.failed';
    } finally {
      shareSaving = false;
    }
  }

  onMount(() => {
    loadProfile();
    loadSharePolicy();
  });
</script>

<div data-testid="settings-view" class="py-10 max-w-4xl mx-auto px-4">
  <div class="card-elevated p-8 rounded-xl">
    <div class="mb-6">
      <h1 class="text-2xl font-bold tracking-tight text-foreground">
        {$t('settings.heading')}
      </h1>
      <p class="mt-1 text-sm text-muted-foreground leading-relaxed">
        {$t('settings.intro')}
      </p>
    </div>

    {#if loading}
      <Skeleton testId="settings-loading" label={$t('common.loading')} />
    {:else}
      {#if generalError}
        <div
          data-testid="settings-general-error"
          class="rounded-xl border border-rose-200 bg-rose-50 px-4 py-3 text-sm text-rose-700 dark:border-rose-900/60 dark:bg-rose-950/40 dark:text-rose-300 shadow-xs mb-6"
          role="alert"
        >
          {$t(generalError)}
        </div>
      {/if}

      {#if savedNotice}
        <div
          data-testid="settings-saved-notice"
          class="rounded-xl border border-emerald-200 bg-emerald-50 px-4 py-3 text-sm text-emerald-800 dark:border-emerald-900/60 dark:bg-emerald-950/40 dark:text-emerald-300 shadow-xs mb-6"
          role="status"
        >
          {$t(savedNotice)}
        </div>
      {/if}

      {#if serverApiAvailable === false}
        <div
          data-testid="settings-api-unavailable"
          class="rounded-xl border border-amber-200 bg-amber-50 px-4 py-3 text-sm text-amber-800 dark:border-amber-900/60 dark:bg-amber-950/40 dark:text-amber-300 shadow-xs mb-6"
          role="note"
        >
          {$t('settings.profile.api_unavailable')}
        </div>
      {/if}

      <!-- 个人基础资料区块：只放 profile 与 locale 字段（密码、两步验证等各有独立区块） -->
      <section class="card-elevated p-6 rounded-xl space-y-6">
        <div>
          <h2 class="text-lg font-bold tracking-tight text-foreground">
            {$t('settings.profile.heading')}
          </h2>
        </div>

        <form onsubmit={handleSubmit} class="space-y-5" novalidate>
          <!-- 显示名 -->
          <div class="block">
            <label for="settings-display-name" class="block text-xs font-semibold uppercase tracking-wider text-muted-foreground mb-1.5">
              {$t('settings.profile.display_name_label')}
            </label>
            <input
              id="settings-display-name"
              data-testid="settings-display-name"
              type="text"
              bind:value={displayName}
              class="field-input text-sm w-full transition-colors"
            />
            {#if fieldErrors.display_name}
              <p data-testid="settings-error-display-name" class="mt-1.5 text-xs text-rose-600 dark:text-rose-400">
                {$t(fieldErrors.display_name)}
              </p>
            {/if}
          </div>

          <!-- 界面语言 -->
          <div class="block">
            <label for="settings-locale" class="block text-xs font-semibold uppercase tracking-wider text-muted-foreground mb-1.5">
              {$t('settings.profile.locale_label')}
            </label>
            <Select
              value={selectedLocale}
              onValueChange={handleLocaleChange}
              testId="settings-locale"
              options={[{ value: 'zh-CN', label: $t('language.zh-CN') }, { value: 'en', label: $t('language.en') }]}
              class="py-2.5"
            />
            {#if fieldErrors.locale}
              <p data-testid="settings-error-locale" class="mt-1.5 text-xs text-rose-600 dark:text-rose-400">
                {$t(fieldErrors.locale)}
              </p>
            {/if}
          </div>

          <!-- 时区 -->
          <div class="block">
            <label for="settings-timezone" class="block text-xs font-semibold uppercase tracking-wider text-muted-foreground mb-1.5">
              {$t('settings.profile.timezone_label')}
            </label>
            <Combobox
              id="settings-timezone"
              testId="settings-timezone"
              bind:value={timezone}
              options={timezoneChoices}
              ariaLabel={$t('settings.profile.timezone_label')}
              class="transition-colors"
            />
            {#if fieldErrors.timezone}
              <p data-testid="settings-error-timezone" class="mt-1.5 text-xs text-rose-600 dark:text-rose-400">
                {$t(fieldErrors.timezone)}
              </p>
            {/if}
          </div>

          <!-- 复习日切点 -->
          <div class="block">
            <label for="settings-cutoff" class="block text-xs font-semibold uppercase tracking-wider text-muted-foreground mb-1.5">
              {$t('settings.profile.cutoff_label')}
            </label>
            <Select
              id="settings-cutoff"
              testId="settings-cutoff"
              bind:value={dayCutoff}
              options={cutoffChoices}
              allowDeselect={false}
              ariaLabel={$t('settings.profile.cutoff_label')}
            />
            <p class="mt-1 text-xs text-muted-foreground/70">
              {$t('settings.profile.cutoff_hint')}
            </p>
            {#if fieldErrors.day_cutoff_hour}
              <p data-testid="settings-error-cutoff" class="mt-1.5 text-xs text-rose-600 dark:text-rose-400">
                {$t(fieldErrors.day_cutoff_hour)}
              </p>
            {/if}
          </div>

          <!-- 提前学习 -->
          <div class="block">
            <label for="settings-learn-ahead" class="block text-xs font-semibold uppercase tracking-wider text-muted-foreground mb-1.5">
              {$t('settings.profile.learn_ahead_label')}
            </label>
            <input
              id="settings-learn-ahead"
              data-testid="settings-learn-ahead"
              type="number"
              min="0"
              max={MAX_LEARN_AHEAD_MINUTES}
              step="1"
              bind:value={learnAhead}
              class="field-input text-sm w-full"
            />
            <p class="mt-1 text-xs text-muted-foreground/70">
              {$t('settings.profile.learn_ahead_hint')}
            </p>
            {#if fieldErrors.learn_ahead_minutes}
              <p data-testid="settings-error-learn-ahead" class="mt-1.5 text-xs text-rose-600 dark:text-rose-400">
                {$t(fieldErrors.learn_ahead_minutes)}
              </p>
            {/if}
          </div>

          <!-- 保存按钮 -->
          <div class="pt-2">
            <Button testId="settings-submit" type="submit" disabled={saving} variant="primary" size="lg">
              {$t(saving ? 'settings.profile.saving' : 'settings.profile.submit')}
            </Button>
          </div>
        </form>
      </section>
      <section class="card-elevated p-6 rounded-xl space-y-5 mt-6" data-testid="settings-password">
        <h2 class="text-lg font-bold tracking-tight text-foreground">{$t('settings.password.heading')}</h2>
        {#if passwordError}<p role="alert">{$t(passwordError)}</p>{/if}
        {#if passwordNotice}<p role="status">{$t(passwordNotice)}</p>{/if}
        <form onsubmit={handlePasswordSubmit} class="space-y-4">
          <label class="block"><span class="block text-xs font-semibold mb-1.5">{$t('settings.password.old_label')}</span><input type="password" autocomplete="current-password" bind:value={oldPassword} required class="field-input text-sm w-full" /></label>
          <label class="block"><span class="block text-xs font-semibold mb-1.5">{$t('settings.password.new_label')}</span><input type="password" autocomplete="new-password" bind:value={newPassword} required class="field-input text-sm w-full" /></label>
          <Button type="submit" disabled={passwordSaving} variant="primary" size="lg">{$t(passwordSaving ? 'settings.password.saving' : 'settings.password.submit')}</Button>
        </form>
      </section>
      <!-- 两步验证入口：服务端 GET /settings/totp 已切到应用壳，走规范路径 -->
      <section class="card-elevated p-6 rounded-xl mt-6" data-testid="settings-totp-entry">
        <h2 class="text-lg font-bold tracking-tight text-foreground">{$t('settings.totp.heading')}</h2>
        <p class="mt-1 text-sm text-muted-foreground leading-relaxed">{$t('settings.totp.intro')}</p>
        <Button href="/settings/totp" variant="primary" size="lg" class="mt-4">{$t('settings.totp.entry')}</Button>
      </section>
      <!-- 邮件通知偏好入口：服务端 GET /settings/notifications 已切到应用壳，走规范路径 -->
      <section class="card-elevated p-6 rounded-xl mt-6" data-testid="settings-notifications-entry">
        <h2 class="text-lg font-bold tracking-tight text-foreground">{$t('settings.notifications.heading')}</h2>
        <p class="mt-1 text-sm text-muted-foreground leading-relaxed">{$t('settings.notifications.intro')}</p>
        <Button href="/settings/notifications" variant="primary" size="lg" class="mt-4">{$t('settings.notifications.entry')}</Button>
      </section>
      <!-- API 密钥入口：服务端 GET /settings/keys 已切到应用壳，走规范路径。
           入口从页头导航移到这里——它属于账号访问凭据，与两步验证、通知偏好同属个人设置。 -->
      <section class="card-elevated p-6 rounded-xl mt-6" data-testid="settings-keys-entry">
        <h2 class="text-lg font-bold tracking-tight text-foreground">{$t('settings.keys.heading')}</h2>
        <p class="mt-1 text-sm text-muted-foreground leading-relaxed">{$t('settings.keys.intro')}</p>
        <Button href="/settings/keys" variant="primary" size="lg" class="mt-4">{$t('settings.keys.entry')}</Button>
      </section>

      <!-- 卡组共享接收策略：在邀请发出之前就拦住，属于「我的偏好」而非卡组设置。 -->
      <section class="card-elevated p-6 rounded-xl mt-6 space-y-4" data-testid="settings-share-policy">
        <div>
          <h2 class="text-lg font-bold tracking-tight text-foreground">{$t('settings.share_policy.heading')}</h2>
          <p class="mt-1 text-sm text-muted-foreground leading-relaxed">{$t('settings.share_policy.intro')}</p>
        </div>

        <!-- 三档接收策略走与同页其余设置项同款的下拉（界面语言、复习日切点、时区），
             不再渲染成竖排三个带边框的单选项：同一页里只有这一处是那种形态。 -->
        <Select
          bind:value={sharePolicy}
          testId="share-policy-options"
          allowDeselect={false}
          ariaLabel={$t('settings.share_policy.heading')}
          disabled={shareSaving}
          class="py-2.5"
          options={[
            { value: 'anyone', label: $t('settings.share_policy.anyone') },
            { value: 'whitelist', label: $t('settings.share_policy.whitelist') },
            { value: 'nobody', label: $t('settings.share_policy.nobody') },
          ]}
          onValueChange={(next) => saveSharePolicy({ policy: next as 'anyone' | 'whitelist' | 'nobody' })}
        />

        {#if sharePolicy === 'whitelist'}
          <div class="pt-1" data-testid="share-policy-whitelist">
            <span class="block text-xs font-semibold mb-1.5 text-foreground/80">{$t('settings.share_policy.allow_label')}</span>
            {#if allowList.length === 0}
              <p class="text-xs text-muted-foreground">{$t('settings.share_policy.allow_empty')}</p>
            {:else}
              <ul class="space-y-1.5">
                {#each allowList as row (row.user_id)}
                  <li class="flex items-center justify-between gap-2 rounded-lg bg-surface px-3 py-1.5">
                    <span class="text-sm text-foreground/90 truncate">{row.username || '#' + row.user_id}</span>
                    <Button
                      testId="share-policy-remove-{row.user_id}"
                      variant="ghost"
                      size="xs"
                      disabled={shareSaving}
                      onclick={() => saveSharePolicy({ revoke: [row.user_id] })}
                    >
                      {$t('settings.share_policy.allow_remove')}
                    </Button>
                  </li>
                {/each}
              </ul>
            {/if}
            <form
              class="mt-2 flex items-center gap-2"
              onsubmit={(event) => {
                event.preventDefault();
                const name = allowName.trim();
                if (name) saveSharePolicy({ allow_usernames: [name] });
              }}
            >
              <input
                type="text"
                bind:value={allowName}
                placeholder={$t('settings.share_policy.allow_placeholder')}
                aria-label={$t('settings.share_policy.allow_placeholder')}
                class="field-input text-sm flex-1 max-w-xs"
                data-testid="share-policy-allow-input"
              />
              <Button type="submit" variant="outline" size="sm" disabled={shareSaving} testId="share-policy-allow-add">
                {$t('settings.share_policy.allow_add')}
              </Button>
            </form>
          </div>
        {/if}

        {#if shareNotice}
          <p class="text-xs text-emerald-600 dark:text-emerald-400" role="status">{$t(shareNotice)}</p>
        {/if}
        {#if shareError}
          <p class="text-xs text-rose-600 dark:text-rose-400" role="alert">{$t(shareError)}</p>
        {/if}
      </section>
    {/if}
  </div>
</div>
