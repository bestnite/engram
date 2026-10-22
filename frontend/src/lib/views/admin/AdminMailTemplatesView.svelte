<script lang="ts">
  import Page from '../../components/ui/Page.svelte';
  import { onMount } from 'svelte';
  import { t } from '../../i18n';
  import { apiClient, ApiClientError } from '../../api';
  import type { AdminMailTemplatesResponse, AdminMailTemplatePreview } from '../../api';
  import AdminNav from './AdminNav.svelte';
  import Button from '../../components/ui/Button.svelte';
  import HelpTip from '../../components/ui/HelpTip.svelte';
  import { toast } from '../../components/ui/toast';
  import Badge from '../../components/ui/Badge.svelte';
  import Select from '../../components/ui/Select.svelte';
  import Skeleton from '../../components/ui/Skeleton.svelte';
  import { draftFor } from '../../mail-template-draft';

  interface Props {
    initialLoading?: boolean;
    initialError?: ApiClientError | Error | null;
    initialData?: AdminMailTemplatesResponse | null;
    /** When embedded in the "mail" page it no longer renders the page-level width, page title and sidebar navigation (nesting twice would duplicate them). */
    embedded?: boolean;
  }

  let { initialLoading = true, initialError = null, initialData = null, embedded = false }: Props = $props();

  // svelte-ignore state_referenced_locally
  let loading = $state(initialLoading);
  // svelte-ignore state_referenced_locally
  let loadError = $state<ApiClientError | Error | null>(initialError);
  // svelte-ignore state_referenced_locally
  let data = $state<AdminMailTemplatesResponse | null>(initialData);

  // The (type, language) the admin explicitly picked; an empty string means "not picked yet", in which case the default is used. The default is **derived** rather than
  // assigned inside onMount: a derived holds during server-side rendering as well (onMount does not run), so the page structure does not depend on a side effect.
  let typeChoice = $state('');
  let localeChoice = $state('');
  let subject = $state('');
  let body = $state('');
  let preview = $state<AdminMailTemplatePreview | null>(null);
  // Which action is in flight: only its button spins, the other buttons are disabled along with it, avoiding two interleaved write operations.
  let pending = $state<'save' | 'restore' | 'preview' | 'test' | null>(null);
  const busy = $derived(pending !== null);
  // Only holds local validation errors from before submission (empty body); request outcomes go through toast.
  let invalid = $state(false);

  const types = $derived(data?.types ?? []);
  const locales = $derived(data?.locales ?? []);
  const selectedType = $derived(typeChoice || types[0]?.type || '');
  const selectedLocale = $derived(localeChoice || data?.site_default_locale || locales[0] || '');
  const currentType = $derived(types.find((item) => item.type === selectedType) ?? null);
  const currentRow = $derived(
    (data?.rows ?? []).find((row) => row.type === selectedType && row.locale === selectedLocale) ?? null
  );
  /** The built-in body for this type and language; it is what the editor is prefilled with when there is no custom template. */
  const currentDefault = $derived(
    (data?.defaults ?? []).find((item) => item.type === selectedType && item.locale === selectedLocale) ?? null
  );
  // The display name is taken from the same-named type on the notification preferences page in the SPA language pack (settings.notifications.type.<type>).
  // The label_key the server provides belongs to the server template's mail.prefs.type.* namespace, and the SPA language pack has no such group of keys,
  // so translating with it directly would show the raw key name to the admin.
  const typeOptions = $derived(types.map((item) => ({ value: item.type, label: $t('settings.notifications.type.' + item.type) })));
  const localeOptions = $derived(locales.map((code) => ({ value: code, label: code })));

  /** Draft a draft: load the custom one if present, otherwise **prefill the built-in default body** (the initial-value rule and its use cases live in
   *  mail-template-draft.ts). It only reads currentRow/currentDefault, so typing cannot feed back and overwrite it. */
  $effect(() => {
    const draft = draftFor(currentRow, currentDefault);
    subject = draft.subject;
    body = draft.body;
    preview = null;
    invalid = false;
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

  /** Map the stable code returned by the server to a localized hint; the English message is not parsed. */
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
    invalid = !body.trim();
    if (invalid) return;
    pending = 'save';
    try {
      await apiClient.saveAdminMailTemplate(selectedType, selectedLocale, { subject, body_md: body });
      await load();
      toast.success($t('admin.mail.saved'));
    } catch (err) {
      toast.error($t(errorKeyFor(err)));
    } finally {
      pending = null;
    }
  }

  async function remove(): Promise<void> {
    pending = 'restore';
    try {
      await apiClient.deleteAdminMailTemplate(selectedType, selectedLocale);
      await load();
      toast.success($t('admin.mail.restored'));
    } catch (err) {
      toast.error($t(errorKeyFor(err)));
    } finally {
      pending = null;
    }
  }

  async function runPreview(): Promise<void> {
    pending = 'preview';
    try {
      preview = await apiClient.previewAdminMailTemplate({
        type: selectedType,
        locale: selectedLocale,
        subject,
        body_md: body,
      });
    } catch (err) {
      toast.error($t(errorKeyFor(err)));
    } finally {
      pending = null;
    }
  }

  async function sendTest(): Promise<void> {
    pending = 'test';
    try {
      await apiClient.testAdminMailTemplate({
        type: selectedType,
        locale: selectedLocale,
        subject,
        body_md: body,
      });
      toast.success($t('admin.mail.test_queued'));
    } catch (err) {
      toast.error($t(errorKeyFor(err)));
    } finally {
      pending = null;
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
        <div class="flex items-center gap-1">
          <label class="text-sm font-medium text-foreground" for="admin-mail-template-body">{$t('admin.mail.body_label')}</label>
          <HelpTip testId="admin-mail-template-body-help" label={$t('admin.mail.body_label')} text={$t('admin.mail.body_hint')} />
        </div>
        <textarea
          id="admin-mail-template-body"
          data-testid="admin-mail-template-body"
          rows="10"
          spellcheck="false"
          bind:value={body}
          class="field-input mt-1.5 w-full font-mono text-sm"
        ></textarea>
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
          <Button variant="primary" size="lg" testId="admin-mail-template-save" loading={pending === 'save'} disabled={busy} onclick={save}>
            {$t('admin.mail.save')}
          </Button>
          <Button variant="outline" size="lg" testId="admin-mail-template-preview" loading={pending === 'preview'} disabled={busy} onclick={runPreview}>
            {$t('admin.mail.preview')}
          </Button>
          <Button variant="outline" size="lg" testId="admin-mail-template-test" loading={pending === 'test'} disabled={busy} onclick={sendTest}>
            {$t('admin.mail.test')}
          </Button>
          <Button variant="ghost" size="lg" testId="admin-mail-template-restore" loading={pending === 'restore'} disabled={busy || !currentRow} onclick={remove} title={$t('admin.mail.restore_hint')}>
            {$t('admin.mail.restore')}
          </Button>
        </div>
        <p class="text-xs text-muted-foreground" data-testid="admin-mail-template-state">
          {currentRow
            ? `${$t('admin.mail.custom_badge')} · ${$t('admin.mail.updated_at', { time: currentRow.updated_at })}`
            : $t('admin.mail.builtin_badge')}
        </p>
        {#if invalid}
          <p role="alert" class="text-sm text-destructive-foreground" data-testid="admin-mail-template-invalid">{$t('admin.mail.empty_body')}</p>
        {/if}
      </div>

      {#if preview}
        <!-- The mail preview is itself "a letter", so a bordered container is kept here to mark the letter's boundaries. -->
        <div class="overflow-hidden rounded-lg border border-border animate-in fade-in-0 duration-200">
          <div class="border-b border-border bg-surface px-4 py-2.5">
            <p class="text-xs text-muted-foreground">{$t('admin.mail.preview_heading')}</p>
            <p class="text-sm font-medium text-foreground" data-testid="admin-mail-template-preview-subject">{preview.subject}</p>
          </div>
          <div class="grid divide-y divide-border lg:grid-cols-2 lg:divide-x lg:divide-y-0">
            <div class="p-4">
              <h3 class="text-xs text-muted-foreground">{$t('admin.mail.preview_html')}</h3>
              <!-- The preview uses server-rendered HTML that has been allowlist-sanitized; as everywhere else in the admin pages, rendering the editor's raw text as HTML is forbidden. -->
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
