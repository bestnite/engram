<script lang="ts">
  import { onMount } from 'svelte';
  import { t } from '../../i18n';
  import { apiClient, ApiClientError } from '../../api';
  import type { AdminMailTemplatesResponse, AdminMailTemplatePreview } from '../../api';
  import AdminNav from './AdminNav.svelte';
  import Panel from '../../components/ui/Panel.svelte';
  import Button from '../../components/ui/Button.svelte';
  import Badge from '../../components/ui/Badge.svelte';
  import Select from '../../components/ui/Select.svelte';
  import Skeleton from '../../components/ui/Skeleton.svelte';
  import { draftFor } from '../../mail-template-draft';

  interface Props {
    initialLoading?: boolean;
    initialError?: ApiClientError | Error | null;
    initialData?: AdminMailTemplatesResponse | null;
    /** 嵌在「邮件」页里时不再渲染页面级宽度、页标题与侧栏导航（套两层会让它们重复）。 */
    embedded?: boolean;
  }

  let { initialLoading = true, initialError = null, initialData = null, embedded = false }: Props = $props();

  // svelte-ignore state_referenced_locally
  let loading = $state(initialLoading);
  // svelte-ignore state_referenced_locally
  let loadError = $state<ApiClientError | Error | null>(initialError);
  // svelte-ignore state_referenced_locally
  let data = $state<AdminMailTemplatesResponse | null>(initialData);

  // 管理员显式选过的 (类型, 语言)；空串表示「还没选」，此时用默认值。默认值用**派生**而不是
  // 在 onMount 里赋值：派生在服务端渲染时同样成立（onMount 不会跑），页面结构不依赖副作用。
  let typeChoice = $state('');
  let localeChoice = $state('');
  let subject = $state('');
  let body = $state('');
  let preview = $state<AdminMailTemplatePreview | null>(null);
  let busy = $state(false);
  let notice = $state('');
  let errorKey = $state('');

  const types = $derived(data?.types ?? []);
  const locales = $derived(data?.locales ?? []);
  const selectedType = $derived(typeChoice || types[0]?.type || '');
  const selectedLocale = $derived(localeChoice || data?.site_default_locale || locales[0] || '');
  const currentType = $derived(types.find((item) => item.type === selectedType) ?? null);
  const currentRow = $derived(
    (data?.rows ?? []).find((row) => row.type === selectedType && row.locale === selectedLocale) ?? null
  );
  /** 该类型该语言的内置正文；没有自定义模板时编辑框预填的就是它。 */
  const currentDefault = $derived(
    (data?.defaults ?? []).find((item) => item.type === selectedType && item.locale === selectedLocale) ?? null
  );
  const typeOptions = $derived(types.map((item) => ({ value: item.type, label: $t(item.label_key) })));
  const localeOptions = $derived(locales.map((code) => ({ value: code, label: code })));

  /** 起草稿：有自定义就载入，没有就**预填内置默认正文**（初值规则与它的用例在
   *  mail-template-draft.ts）。它只读 currentRow/currentDefault，所以不会因为打字而回灌覆盖。 */
  $effect(() => {
    const draft = draftFor(currentRow, currentDefault);
    subject = draft.subject;
    body = draft.body;
    preview = null;
    notice = '';
    errorKey = '';
  });

  function loadErrorKey(): string {
    if (loadError instanceof ApiClientError && (loadError.isUnauthorized || loadError.status === 403)) {
      return 'admin.error.forbidden';
    }
    return 'admin.mail.load_failed';
  }

  async function load(): Promise<void> {
    loading = true;
    loadError = null;
    try {
      data = await apiClient.getAdminMailTemplates();
    } catch (err) {
      loadError = err instanceof Error ? err : new Error(String(err));
    } finally {
      loading = false;
    }
  }

  onMount(() => {
    if (initialData === null && initialError === null) {
      void load();
    }
  });

  /** 把服务端返回的稳定 code 映射到本地化提示；不解析英文 message。 */
  function errorKeyFor(err: unknown): string {
    const code = err instanceof ApiClientError ? err.code : '';
    switch (code) {
      case 'invalid_template':
        return 'admin.mail.error.invalid_template';
      case 'mail_not_configured':
        return 'admin.mail.error.mail_not_configured';
      case 'save_failed':
      case 'delete_failed':
        return 'admin.mail.error.save_failed';
      default:
        return 'admin.mail.error.generic';
    }
  }

  async function save(): Promise<void> {
    if (!body.trim()) {
      errorKey = 'admin.mail.empty_body';
      return;
    }
    busy = true;
    notice = '';
    errorKey = '';
    try {
      await apiClient.saveAdminMailTemplate(selectedType, selectedLocale, { subject, body_md: body });
      await load();
      notice = 'admin.mail.saved';
    } catch (err) {
      errorKey = errorKeyFor(err);
    } finally {
      busy = false;
    }
  }

  async function remove(): Promise<void> {
    busy = true;
    notice = '';
    errorKey = '';
    try {
      await apiClient.deleteAdminMailTemplate(selectedType, selectedLocale);
      await load();
      notice = 'admin.mail.restored';
    } catch (err) {
      errorKey = errorKeyFor(err);
    } finally {
      busy = false;
    }
  }

  async function runPreview(): Promise<void> {
    busy = true;
    errorKey = '';
    try {
      preview = await apiClient.previewAdminMailTemplate({
        type: selectedType,
        locale: selectedLocale,
        subject,
        body_md: body,
      });
    } catch (err) {
      errorKey = errorKeyFor(err);
    } finally {
      busy = false;
    }
  }

  async function sendTest(): Promise<void> {
    busy = true;
    notice = '';
    errorKey = '';
    try {
      await apiClient.testAdminMailTemplate({
        type: selectedType,
        locale: selectedLocale,
        subject,
        body_md: body,
      });
      notice = 'admin.mail.test_queued';
    } catch (err) {
      errorKey = errorKeyFor(err);
    } finally {
      busy = false;
    }
  }
</script>

<div class={embedded ? 'space-y-5' : 'py-10 max-w-5xl mx-auto px-4'} data-testid="admin-mail-templates">
  {#if !embedded}
    <h1 class="text-2xl font-bold tracking-tight text-zinc-900 dark:text-zinc-100">{$t('admin.mail.heading')}</h1>
    <p class="mt-2 text-sm text-zinc-500 dark:text-zinc-400">{$t('admin.mail.intro')}</p>

    <div class="mt-6">
      <AdminNav />
    </div>
  {/if}

  {#if loading}
    <div class="mt-6">
      <Skeleton testId="admin-mail-templates-loading" label={$t('common.loading')} lines={3} />
    </div>
  {:else if loadError}
    <Panel class="mt-6 text-center">
      <p role="alert" class="text-sm text-rose-600 dark:text-rose-400" data-testid="admin-mail-templates-failed">
        {$t(loadErrorKey())}
      </p>
      <Button variant="primary" size="lg" testId="admin-mail-templates-retry" class="mt-4" onclick={load}>
        {$t('common.retry')}
      </Button>
    </Panel>
  {:else if data}
    <div class="mt-6 space-y-5">
      <Panel class="space-y-4">
        <div class="grid gap-4 sm:grid-cols-2">
          <div>
            <span class="block text-sm font-medium text-zinc-700 dark:text-zinc-300">{$t('admin.mail.type_label')}</span>
            <Select
              class="mt-1 w-full"
              testId="admin-mail-template-type"
              value={selectedType}
              onValueChange={(value: string) => (typeChoice = value)}
              options={typeOptions}
            />
          </div>
          <div>
            <span class="block text-sm font-medium text-zinc-700 dark:text-zinc-300">{$t('admin.mail.locale_label')}</span>
            <Select
              class="mt-1 w-full"
              testId="admin-mail-template-locale"
              value={selectedLocale}
              onValueChange={(value: string) => (localeChoice = value)}
              options={localeOptions}
            />
          </div>
        </div>

        <p class="text-xs text-zinc-500 dark:text-zinc-400" data-testid="admin-mail-template-fallback">
          {$t('admin.mail.fallback_hint', { locale: data.site_default_locale || '—' })}
        </p>

        <p class="text-xs text-zinc-500 dark:text-zinc-400" data-testid="admin-mail-template-prefill">
          {$t(currentRow ? 'admin.mail.prefill_custom' : 'admin.mail.prefill_builtin')}
        </p>

        {#if currentType}
          <div data-testid="admin-mail-template-vars">
            <span class="block text-sm font-medium text-zinc-700 dark:text-zinc-300">{$t('admin.mail.vars_heading')}</span>
            <ul class="mt-2 space-y-1.5">
              {#each currentType.vars as variable (variable.name)}
                <li class="flex flex-wrap items-baseline gap-2 text-xs">
                  <code class="rounded-md bg-zinc-100 dark:bg-zinc-800 px-1.5 py-0.5 font-mono text-zinc-800 dark:text-zinc-200"
                    >{`{{${variable.name}}}`}</code
                  >
                  {#if variable.required}
                    <Badge variant="warning" testId="admin-mail-template-var-required">{variable.name}</Badge>
                  {/if}
                  <span class="text-zinc-500 dark:text-zinc-400">{$t(variable.note_key)}</span>
                </li>
              {/each}
            </ul>
          </div>
        {/if}
      </Panel>

      <Panel class="space-y-4">
        <div>
          <label class="block text-sm font-medium text-zinc-700 dark:text-zinc-300" for="admin-mail-template-subject">
            {$t('admin.mail.subject_label')}
          </label>
          <input
            id="admin-mail-template-subject"
            data-testid="admin-mail-template-subject"
            bind:value={subject}
            class="field-input text-sm mt-1.5 w-full"
          />
          <p class="mt-1 text-xs text-zinc-500 dark:text-zinc-400">{$t('admin.mail.subject_hint')}</p>
        </div>
        <div>
          <label class="block text-sm font-medium text-zinc-700 dark:text-zinc-300" for="admin-mail-template-body">
            {$t('admin.mail.body_label')}
          </label>
          <textarea
            id="admin-mail-template-body"
            data-testid="admin-mail-template-body"
            rows="10"
            spellcheck="false"
            bind:value={body}
            class="field-input text-sm mt-1.5 w-full font-mono"
          ></textarea>
          <p class="mt-1 text-xs text-zinc-500 dark:text-zinc-400">{$t('admin.mail.body_hint')}</p>
        </div>

        <div class="flex flex-wrap items-center gap-3">
          <Button variant="primary" size="lg" testId="admin-mail-template-save" disabled={busy} onclick={save}>
            {busy ? $t('admin.mail.saving') : $t('admin.mail.save')}
          </Button>
          <Button variant="outline" size="lg" testId="admin-mail-template-preview" disabled={busy} onclick={runPreview}>
            {$t('admin.mail.preview')}
          </Button>
          <Button variant="outline" size="lg" testId="admin-mail-template-test" disabled={busy} onclick={sendTest}>
            {$t('admin.mail.test')}
          </Button>
          <Button
            variant="outline"
            size="lg"
            testId="admin-mail-template-restore"
            disabled={busy || !currentRow}
            onclick={remove}
          >
            {$t('admin.mail.restore')}
          </Button>
        </div>
        <p class="text-xs text-zinc-500 dark:text-zinc-400">{$t('admin.mail.restore_hint')}</p>

        <p class="text-sm text-zinc-500 dark:text-zinc-400" data-testid="admin-mail-template-state">
          {currentRow
            ? `${$t('admin.mail.custom_badge')} · ${$t('admin.mail.updated_at', { time: currentRow.updated_at })}`
            : $t('admin.mail.builtin_badge')}
        </p>
        {#if notice}
          <p role="status" class="text-sm text-emerald-700 dark:text-emerald-400" data-testid="admin-mail-template-notice">
            {$t(notice)}
          </p>
        {/if}
        {#if errorKey}
          <p role="alert" class="text-sm text-rose-600 dark:text-rose-400" data-testid="admin-mail-template-error">
            {$t(errorKey)}
          </p>
        {/if}
      </Panel>

      {#if preview}
        <Panel class="space-y-4" padding="md">
          <h2 class="text-sm font-semibold text-zinc-900 dark:text-zinc-100">{$t('admin.mail.preview_heading')}</h2>
          <p class="text-sm text-zinc-700 dark:text-zinc-300" data-testid="admin-mail-template-preview-subject">
            {preview.subject}
          </p>
          <div>
            <h3 class="text-xs font-semibold uppercase tracking-wider text-zinc-500 dark:text-zinc-400">
              {$t('admin.mail.preview_html')}
            </h3>
            <!-- 预览用服务端渲染并经白名单清洗的 HTML；与管理页其它地方一样，禁止把编辑器原文当 HTML 渲染。 -->
            <div
              class="mt-2 rounded-xl border border-zinc-200 dark:border-zinc-700 bg-white p-4"
              data-testid="admin-mail-template-preview-html">{@html preview.html}</div
            >
          </div>
          <div>
            <h3 class="text-xs font-semibold uppercase tracking-wider text-zinc-500 dark:text-zinc-400">
              {$t('admin.mail.preview_text')}
            </h3>
            <pre
              class="mt-2 overflow-x-auto rounded-xl border border-zinc-200 dark:border-zinc-700 bg-zinc-50 dark:bg-zinc-900 p-4 text-xs text-zinc-700 dark:text-zinc-300"
              data-testid="admin-mail-template-preview-text">{preview.text}</pre>
          </div>
        </Panel>
      {/if}
    </div>
  {/if}
</div>
