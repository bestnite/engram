<script lang="ts">
  import Page from '../../components/ui/Page.svelte';
  import { onMount } from 'svelte';
  import { t } from '../../i18n';
  import { apiClient, ApiClientError } from '../../api';
  import type { AdminMailTemplatesResponse, AdminMailTemplatePreview } from '../../api';
  import AdminNav from './AdminNav.svelte';
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

<Page {embedded} class={embedded ? 'space-y-5' : 'space-y-6'} testId="admin-mail-templates">
  {#if !embedded}
    <h1 class="text-2xl font-semibold tracking-tight text-foreground">{$t('admin.mail.heading')}</h1>
    <p class="mt-2 text-sm text-muted-foreground">{$t('admin.mail.intro')}</p>

    <div class="mt-6">
      <AdminNav />
    </div>
  {/if}

  {#if loading}
    <Skeleton testId="admin-mail-templates-loading" label={$t('common.loading')} lines={3} />
  {:else if loadError}
    <div class="py-8 text-center">
      <p role="alert" class="text-sm text-destructive-foreground" data-testid="admin-mail-templates-failed">
        {$t(loadErrorKey())}
      </p>
      <Button variant="outline" size="lg" testId="admin-mail-templates-retry" class="mt-4" onclick={load}>
        {$t('common.retry')}
      </Button>
    </div>
  {:else if data}
    <div class="space-y-6">
      <div class="grid gap-5 sm:grid-cols-2">
        <div>
          <span class="block text-sm font-medium text-foreground">{$t('admin.mail.type_label')}</span>
          <Select
            class="mt-1.5 w-full"
            testId="admin-mail-template-type"
            value={selectedType}
            onValueChange={(value: string) => (typeChoice = value)}
            options={typeOptions}
            ariaLabel={$t('admin.mail.type_label')}
          />
        </div>
        <div>
          <span class="block text-sm font-medium text-foreground">{$t('admin.mail.locale_label')}</span>
          <Select
            class="mt-1.5 w-full"
            testId="admin-mail-template-locale"
            value={selectedLocale}
            onValueChange={(value: string) => (localeChoice = value)}
            options={localeOptions}
            ariaLabel={$t('admin.mail.locale_label')}
          />
        </div>
      </div>

      <div class="space-y-1 text-xs text-muted-foreground">
        <p data-testid="admin-mail-template-fallback">{$t('admin.mail.fallback_hint', { locale: data.site_default_locale || '—' })}</p>
        <p data-testid="admin-mail-template-prefill">{$t(currentRow ? 'admin.mail.prefill_custom' : 'admin.mail.prefill_builtin')}</p>
      </div>

      <div>
        <label class="block text-sm font-medium text-foreground" for="admin-mail-template-subject">{$t('admin.mail.subject_label')}</label>
        <input
          id="admin-mail-template-subject"
          data-testid="admin-mail-template-subject"
          bind:value={subject}
          class="field-input mt-1.5 w-full text-sm"
        />
        <p class="mt-1.5 text-xs text-muted-foreground">{$t('admin.mail.subject_hint')}</p>
      </div>
      <div>
        <label class="block text-sm font-medium text-foreground" for="admin-mail-template-body">{$t('admin.mail.body_label')}</label>
        <textarea
          id="admin-mail-template-body"
          data-testid="admin-mail-template-body"
          rows="10"
          spellcheck="false"
          bind:value={body}
          class="field-input mt-1.5 w-full font-mono text-sm"
        ></textarea>
        <p class="mt-1.5 text-xs text-muted-foreground">{$t('admin.mail.body_hint')}</p>
      </div>

      {#if currentType}
        <div data-testid="admin-mail-template-vars">
          <span class="block text-sm font-medium text-foreground">{$t('admin.mail.vars_heading')}</span>
          <ul class="mt-2 divide-y divide-border rounded-lg border border-border">
            {#each currentType.vars as variable (variable.name)}
              <li class="flex flex-wrap items-baseline gap-2 px-3 py-2 text-xs">
                <code class="rounded bg-muted px-1.5 py-0.5 font-mono text-foreground">{`{{${variable.name}}}`}</code>
                {#if variable.required}
                  <Badge variant="warning" testId="admin-mail-template-var-required">{variable.name}</Badge>
                {/if}
                <span class="text-muted-foreground">{$t(variable.note_key)}</span>
              </li>
            {/each}
          </ul>
        </div>
      {/if}

      <div class="space-y-2">
        <div class="flex flex-wrap items-center gap-2">
          <Button variant="primary" size="lg" testId="admin-mail-template-save" disabled={busy} onclick={save}>
            {busy ? $t('admin.mail.saving') : $t('admin.mail.save')}
          </Button>
          <Button variant="outline" size="lg" testId="admin-mail-template-preview" disabled={busy} onclick={runPreview}>
            {$t('admin.mail.preview')}
          </Button>
          <Button variant="outline" size="lg" testId="admin-mail-template-test" disabled={busy} onclick={sendTest}>
            {$t('admin.mail.test')}
          </Button>
          <Button variant="ghost" size="lg" testId="admin-mail-template-restore" disabled={busy || !currentRow} onclick={remove} title={$t('admin.mail.restore_hint')}>
            {$t('admin.mail.restore')}
          </Button>
        </div>
        <p class="text-xs text-muted-foreground" data-testid="admin-mail-template-state">
          {currentRow
            ? `${$t('admin.mail.custom_badge')} · ${$t('admin.mail.updated_at', { time: currentRow.updated_at })}`
            : $t('admin.mail.builtin_badge')}
        </p>
        {#if notice}
          <p role="status" class="text-sm text-success" data-testid="admin-mail-template-notice">{$t(notice)}</p>
        {/if}
        {#if errorKey}
          <p role="alert" class="text-sm text-destructive-foreground" data-testid="admin-mail-template-error">{$t(errorKey)}</p>
        {/if}
      </div>

      {#if preview}
        <!-- 邮件预览本身就是「一封信」，这里保留一个带边框的容器表示信的边界。 -->
        <div class="overflow-hidden rounded-lg border border-border animate-in fade-in-0 duration-200">
          <div class="border-b border-border bg-surface px-4 py-2.5">
            <p class="text-xs text-muted-foreground">{$t('admin.mail.preview_heading')}</p>
            <p class="text-sm font-medium text-foreground" data-testid="admin-mail-template-preview-subject">{preview.subject}</p>
          </div>
          <div class="grid divide-y divide-border lg:grid-cols-2 lg:divide-x lg:divide-y-0">
            <div class="p-4">
              <h3 class="text-xs text-muted-foreground">{$t('admin.mail.preview_html')}</h3>
              <!-- 预览用服务端渲染并经白名单清洗的 HTML；与管理页其它地方一样，禁止把编辑器原文当 HTML 渲染。 -->
              <div class="mt-2 rounded-md bg-white p-4 text-zinc-900" data-testid="admin-mail-template-preview-html">{@html preview.html}</div>
            </div>
            <div class="p-4">
              <h3 class="text-xs text-muted-foreground">{$t('admin.mail.preview_text')}</h3>
              <pre class="mt-2 overflow-x-auto whitespace-pre-wrap rounded-md bg-surface p-4 text-xs text-foreground/80" data-testid="admin-mail-template-preview-text">{preview.text}</pre>
            </div>
          </div>
        </div>
      {/if}
    </div>
  {/if}
</Page>
