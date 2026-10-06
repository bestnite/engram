<script lang="ts">
  import { onMount } from 'svelte';
  import { t, localeStore, setLocale, type SupportedLocale, isSupportedLocale } from '../i18n';
  import {
    apiClient,
    ApiClientError,
    getApiErrorMessageKey,
    validateProfileForm,
    COMMON_TIMEZONES,
    type UserProfile,
  } from '../api';

  // 视图响应式状态（Svelte 5 runes）
  let loading = $state(true);
  let saving = $state(false);
  let serverApiAvailable = $state<boolean | null>(null);
  let savedNotice = $state<string | null>(null);
  let generalError = $state<string | null>(null);
  let passwordError = $state<string | null>(null);
  let passwordNotice = $state<string | null>(null);
  let passwordSaving = $state(false);
  let oldPassword = $state('');
  let newPassword = $state('');
  let fieldErrors = $state<Partial<Record<'display_name' | 'locale' | 'timezone' | 'day_cutoff_hour', string>>>({});

  // 表单字段绑定
  let displayName = $state('');
  let selectedLocale = $state<SupportedLocale>($localeStore);
  let timezone = $state('');
  let dayCutoff = $state('');

  /**
   * 初始化探测与加载已有用户数据（DESIGN.md §4.1、§8.3）
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
      if (profile.day_cutoff_hour !== null && profile.day_cutoff_hour !== undefined) {
        dayCutoff = String(profile.day_cutoff_hour);
      } else {
        dayCutoff = '';
      }
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
   * 切换界面语言：前端 UI 即时同步更新响应式 store（DESIGN.md §8.3）
   */
  function handleLocaleChange(event: Event): void {
    const target = event.target as HTMLSelectElement;
    const nextLocale = target.value;
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

  /**
   * 提交个人资料与设置
   */
  async function handleSubmit(event: Event): Promise<void> {
    event.preventDefault();
    savedNotice = null;
    generalError = null;
    fieldErrors = {};

    const rawCutoff = dayCutoff.trim() === '' ? null : dayCutoff;
    const validation = validateProfileForm({
      display_name: displayName,
      locale: selectedLocale,
      timezone,
      day_cutoff_hour: rawCutoff,
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

  onMount(() => {
    loadProfile();
  });
</script>

<div data-testid="settings-view" class="py-10 max-w-4xl mx-auto px-4">
  <div class="card-elevated p-8 rounded-xl">
    <div class="mb-6">
      <h1 class="text-2xl font-bold tracking-tight text-zinc-900 dark:text-zinc-100">
        {$t('settings.heading')}
      </h1>
      <p class="mt-1 text-sm text-zinc-600 dark:text-zinc-400 leading-relaxed">
        {$t('settings.intro')}
      </p>
    </div>

    {#if loading}
      <div data-testid="settings-loading" class="py-12 text-center text-zinc-500 dark:text-zinc-400">
        <div class="inline-block animate-spin w-6 h-6 border-2 border-current border-t-transparent rounded-full mb-3" aria-hidden="true"></div>
        <p class="text-sm">{$t('common.loading')}</p>
      </div>
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
      <section class="card-subtle p-6 rounded-xl space-y-6">
        <div>
          <h2 class="text-lg font-bold tracking-tight text-zinc-900 dark:text-zinc-100">
            {$t('settings.profile.heading')}
          </h2>
        </div>

        <form onsubmit={handleSubmit} class="space-y-5" novalidate>
          <!-- 显示名 -->
          <div class="block">
            <label for="settings-display-name" class="block text-xs font-semibold uppercase tracking-wider text-zinc-500 dark:text-zinc-400 mb-1.5">
              {$t('settings.profile.display_name_label')}
            </label>
            <input
              id="settings-display-name"
              data-testid="settings-display-name"
              type="text"
              bind:value={displayName}
              class="w-full rounded-xl border border-zinc-200 bg-white px-3.5 py-2.5 text-sm text-zinc-900 shadow-2xs focus:border-zinc-900 focus:outline-none focus:ring-1 focus:ring-zinc-900 dark:border-zinc-700 dark:bg-zinc-900 dark:text-zinc-100 dark:focus:border-zinc-100 dark:focus:ring-zinc-100 transition-colors"
            />
            {#if fieldErrors.display_name}
              <p data-testid="settings-error-display-name" class="mt-1.5 text-xs text-rose-600 dark:text-rose-400">
                {$t(fieldErrors.display_name)}
              </p>
            {/if}
          </div>

          <!-- 界面语言 -->
          <div class="block">
            <label for="settings-locale" class="block text-xs font-semibold uppercase tracking-wider text-zinc-500 dark:text-zinc-400 mb-1.5">
              {$t('settings.profile.locale_label')}
            </label>
            <select
              id="settings-locale"
              data-testid="settings-locale"
              value={selectedLocale}
              onchange={handleLocaleChange}
              class="w-full rounded-xl border border-zinc-200 bg-white px-3.5 py-2.5 text-sm text-zinc-900 shadow-2xs focus:border-zinc-900 focus:outline-none focus:ring-1 focus:ring-zinc-900 dark:border-zinc-700 dark:bg-zinc-900 dark:text-zinc-100 dark:focus:border-zinc-100 dark:focus:ring-zinc-100 transition-colors cursor-pointer"
            >
              <option value="zh-CN">{$t('language.zh-CN')}</option>
              <option value="en">{$t('language.en')}</option>
            </select>
            {#if fieldErrors.locale}
              <p data-testid="settings-error-locale" class="mt-1.5 text-xs text-rose-600 dark:text-rose-400">
                {$t(fieldErrors.locale)}
              </p>
            {/if}
          </div>

          <!-- 时区 -->
          <div class="block">
            <label for="settings-timezone" class="block text-xs font-semibold uppercase tracking-wider text-zinc-500 dark:text-zinc-400 mb-1.5">
              {$t('settings.profile.timezone_label')}
            </label>
            <input
              id="settings-timezone"
              data-testid="settings-timezone"
              type="text"
              bind:value={timezone}
              list="settings-timezone-options"
              class="w-full rounded-xl border border-zinc-200 bg-white px-3.5 py-2.5 text-sm text-zinc-900 shadow-2xs focus:border-zinc-900 focus:outline-none focus:ring-1 focus:ring-zinc-900 dark:border-zinc-700 dark:bg-zinc-900 dark:text-zinc-100 dark:focus:border-zinc-100 dark:focus:ring-zinc-100 transition-colors"
            />
            <datalist id="settings-timezone-options">
              {#each COMMON_TIMEZONES as tz}
                <option value={tz}></option>
              {/each}
            </datalist>
            <p class="mt-1 text-xs text-zinc-400 dark:text-zinc-500">
              {$t('settings.profile.timezone_hint')}
            </p>
            {#if fieldErrors.timezone}
              <p data-testid="settings-error-timezone" class="mt-1.5 text-xs text-rose-600 dark:text-rose-400">
                {$t(fieldErrors.timezone)}
              </p>
            {/if}
          </div>

          <!-- 复习日切点 -->
          <div class="block">
            <label for="settings-cutoff" class="block text-xs font-semibold uppercase tracking-wider text-zinc-500 dark:text-zinc-400 mb-1.5">
              {$t('settings.profile.cutoff_label')}
            </label>
            <input
              id="settings-cutoff"
              data-testid="settings-cutoff"
              type="number"
              min="0"
              max="23"
              bind:value={dayCutoff}
              class="w-32 rounded-xl border border-zinc-200 bg-white px-3.5 py-2.5 text-sm text-zinc-900 font-mono shadow-2xs focus:border-zinc-900 focus:outline-none focus:ring-1 focus:ring-zinc-900 dark:border-zinc-700 dark:bg-zinc-900 dark:text-zinc-100 dark:focus:border-zinc-100 dark:focus:ring-zinc-100 transition-colors"
            />
            <p class="mt-1 text-xs text-zinc-400 dark:text-zinc-500">
              {$t('settings.profile.cutoff_hint')}
            </p>
            {#if fieldErrors.day_cutoff_hour}
              <p data-testid="settings-error-cutoff" class="mt-1.5 text-xs text-rose-600 dark:text-rose-400">
                {$t(fieldErrors.day_cutoff_hour)}
              </p>
            {/if}
          </div>

          <!-- 保存按钮 -->
          <div class="pt-2">
            <button
              data-testid="settings-submit"
              type="submit"
              disabled={saving}
              class="inline-flex items-center justify-center rounded-xl bg-zinc-950 px-5 py-2.5 text-sm font-semibold text-white shadow-xs hover:bg-zinc-800 dark:bg-zinc-100 dark:text-zinc-950 dark:hover:bg-white active:scale-[0.98] transition-all cursor-pointer disabled:opacity-50 disabled:cursor-not-allowed"
            >
              {$t(saving ? 'settings.profile.saving' : 'settings.profile.submit')}
            </button>
          </div>
        </form>
      </section>
      <section class="card-subtle p-6 rounded-xl space-y-5 mt-6" data-testid="settings-password">
        <h2 class="text-lg font-bold tracking-tight text-zinc-900 dark:text-zinc-100">{$t('settings.password.heading')}</h2>
        {#if passwordError}<p role="alert">{$t(passwordError)}</p>{/if}
        {#if passwordNotice}<p role="status">{$t(passwordNotice)}</p>{/if}
        <form onsubmit={handlePasswordSubmit} class="space-y-4">
          <label class="block"><span class="block text-xs font-semibold mb-1.5">{$t('settings.password.old_label')}</span><input type="password" autocomplete="current-password" bind:value={oldPassword} required class="w-full rounded-xl border px-3.5 py-2.5 text-sm" /></label>
          <label class="block"><span class="block text-xs font-semibold mb-1.5">{$t('settings.password.new_label')}</span><input type="password" autocomplete="new-password" bind:value={newPassword} required class="w-full rounded-xl border px-3.5 py-2.5 text-sm" /></label>
          <button type="submit" disabled={passwordSaving} class="rounded-xl bg-zinc-950 px-5 py-2.5 text-sm font-semibold text-white disabled:opacity-50">{$t(passwordSaving ? 'settings.password.saving' : 'settings.password.submit')}</button>
        </form>
      </section>
      <!-- 两步验证入口：服务端 GET /settings/totp 已切到应用壳，走规范路径 -->
      <section class="card-subtle p-6 rounded-xl mt-6" data-testid="settings-totp-entry">
        <h2 class="text-lg font-bold tracking-tight text-zinc-900 dark:text-zinc-100">{$t('settings.totp.heading')}</h2>
        <p class="mt-1 text-sm text-zinc-600 dark:text-zinc-400 leading-relaxed">{$t('settings.totp.intro')}</p>
        <a href="/settings/totp" class="mt-4 inline-block rounded-xl bg-zinc-950 px-5 py-2.5 text-sm font-semibold text-white shadow-xs hover:bg-zinc-800 dark:bg-zinc-100 dark:text-zinc-950 dark:hover:bg-white transition-colors">{$t('settings.totp.entry')}</a>
      </section>
      <!-- 邮件通知偏好入口：服务端 GET /settings/notifications 已切到应用壳，走规范路径 -->
      <section class="card-subtle p-6 rounded-xl mt-6" data-testid="settings-notifications-entry">
        <h2 class="text-lg font-bold tracking-tight text-zinc-900 dark:text-zinc-100">{$t('settings.notifications.heading')}</h2>
        <p class="mt-1 text-sm text-zinc-600 dark:text-zinc-400 leading-relaxed">{$t('settings.notifications.intro')}</p>
        <a href="/settings/notifications" class="mt-4 inline-block rounded-xl bg-zinc-950 px-5 py-2.5 text-sm font-semibold text-white shadow-xs hover:bg-zinc-800 dark:bg-zinc-100 dark:text-zinc-950 dark:hover:bg-white transition-colors">{$t('settings.notifications.entry')}</a>
      </section>
    {/if}
  </div>
</div>
