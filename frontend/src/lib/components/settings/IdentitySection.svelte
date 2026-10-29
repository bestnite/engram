<script lang="ts">
  import { onMount } from 'svelte';
  import { t, getLocale } from '../../i18n';
  import { apiClient, ApiClientError } from '../../api';
  import type { IdentitiesResponse, IdentityItem } from '../../api';
  import { authStore } from '../../auth';
  import SettingsSection from '../ui/SettingsSection.svelte';
  import Button from '../ui/Button.svelte';
  import Skeleton from '../ui/Skeleton.svelte';
  import { toast } from '../ui/toast';
  import { askConfirm } from '../ui/confirm';
  import { RefreshCw } from '@lucide/svelte';

  /**
   * Linked sign-in identities (security page): lists the OIDC identities bound to the account, starts a new link, and removes one.
   *
   * The bind flow leaves the SPA for the provider's authorize URL, so navigation is an injectable prop: the component has no
   * other reason to touch window.location, and a mount test can observe the destination without a real navigation.
   *
   * `initial` exists for SSR tests: when it is provided the component renders that state and does not fetch; when it is null
   * (the default) the component fetches on mount.
   */
  interface Props {
    initial?: IdentitiesResponse | null;
    /** Where a successful bind sends the browser; the default performs the real navigation. */
    navigate?: (url: string) => void;
  }

  let {
    initial = null,
    navigate = (url: string) => {
      window.location.assign(url);
    },
  }: Props = $props();

  // svelte-ignore state_referenced_locally
  let data = $state<IdentitiesResponse | null>(initial);
  // svelte-ignore state_referenced_locally
  let loading = $state(initial === null);
  let loadError = $state<Error | null>(null);
  let binding = $state(false);
  let unlinkingId = $state<string | null>(null);

  /** Only a passwordless account whose single identity is its last way in cannot remove that identity. */
  const lastCredential = $derived(
    data?.identities.length === 1 && $authStore.user?.has_password === false
  );

  const hidden = $derived(
    data !== null && !data.oidc_enabled && data.identities.length === 0
  );

  function formatLinkedAt(value: string): string {
    const date = new Date(value);
    if (Number.isNaN(date.getTime())) return value;
    return new Intl.DateTimeFormat(getLocale(), { year: 'numeric', month: 'short', day: 'numeric' }).format(date);
  }

  async function load(): Promise<void> {
    loading = true;
    loadError = null;
    try {
      data = await apiClient.getIdentities();
    } catch (err) {
      loadError = err instanceof Error ? err : new Error(String(err));
    } finally {
      loading = false;
    }
  }

  /**
   * The provider redirects back to ?identity=..., so the outcome is reported once here and the query is removed from the
   * address: a reload would otherwise repeat a toast about an event that already ended.
   */
  function consumeIdentityQuery(): void {
    if (typeof window === 'undefined') return;
    const params = new URLSearchParams(window.location.search);
    const outcome = params.get('identity');
    if (!outcome) return;
    if (outcome === 'linked') {
      toast.success($t('settings.identity.toast.linked'));
    } else if (outcome === 'taken') {
      toast.error($t('settings.identity.toast.taken'));
    } else if (outcome === 'failed') {
      toast.error($t('settings.identity.toast.failed'));
    } else if (outcome === 'session') {
      toast.error($t('settings.identity.toast.session'));
    }
    params.delete('identity');
    const query = params.toString();
    const cleaned = `${window.location.pathname}${query ? `?${query}` : ''}${window.location.hash}`;
    history.replaceState(null, '', cleaned);
  }

  async function bind(): Promise<void> {
    binding = true;
    try {
      const res = await apiClient.beginIdentityBind();
      // Keep the loading state: the page is about to be replaced by the provider's page.
      navigate(res.authorize_url);
    } catch (err) {
      binding = false;
      const code = err instanceof ApiClientError ? err.code : '';
      if (code === 'oidc_unavailable') {
        toast.error($t('settings.identity.bind_unavailable'));
      } else if (code === 'busy') {
        toast.error($t('settings.identity.bind_busy'));
      } else {
        toast.error($t('settings.identity.bind_failed'));
      }
    }
  }

  async function unlink(identity: IdentityItem): Promise<void> {
    const confirmed = await askConfirm({
      title: $t('settings.identity.confirm.unlink_title'),
      description: $t('settings.identity.confirm.unlink_desc'),
      confirmLabel: $t('settings.identity.unlink'),
      destructive: true,
    });
    if (!confirmed) return;
    unlinkingId = identity.id;
    try {
      await apiClient.unlinkIdentity(identity.id);
      toast.success($t('settings.identity.toast.unlinked'));
      await load();
    } catch (err) {
      const code = err instanceof ApiClientError ? err.code : '';
      toast.error($t(code === 'last_credential' ? 'settings.identity.last_credential' : 'settings.identity.unlink_failed'));
    } finally {
      unlinkingId = null;
    }
  }

  onMount(() => {
    consumeIdentityQuery();
    if (initial === null) {
      void load();
    }
  });
</script>

{#if !hidden}
  <SettingsSection
    title={$t('settings.identity.heading')}
    description={$t('settings.identity.intro')}
    testId="settings-identities"
  >
    {#if loading}
      <Skeleton testId="identities-loading" label={$t('common.loading')} />
    {:else if loadError}
      <p role="alert" data-testid="identities-failed" class="text-sm text-warning">{$t('settings.identity.load_failed')}</p>
      <Button type="button" testId="identities-retry" variant="outline" size="sm" class="mt-3" onclick={() => load()}>
        <RefreshCw class="size-4" aria-hidden="true" />
        <span>{$t('common.retry')}</span>
      </Button>
    {:else if data}
      {#if data.identities.length === 0}
        <p data-testid="identities-empty" class="text-sm text-muted-foreground">{$t('settings.identity.empty')}</p>
      {:else}
        <!-- The settings column can be narrower than this table, and a provider name or email may be one long unbroken
             token. The wrapper scrolls rather than squeezing the columns, and cells wrap mid-word instead of pushing the
             unlink button out of reach. -->
        <div class="-mx-1 overflow-x-auto px-1">
          <table class="w-full min-w-[34rem] max-w-2xl text-sm">
            <thead>
              <tr class="text-left text-xs text-muted-foreground">
                <th class="pb-2 pr-4 font-medium">{$t('settings.identity.col.provider')}</th>
                <th class="pb-2 pr-4 font-medium">{$t('settings.identity.col.email')}</th>
                <th class="pb-2 pr-4 font-medium">{$t('settings.identity.col.linked')}</th>
                <th class="pb-2"><span class="sr-only">{$t('settings.identity.unlink')}</span></th>
              </tr>
            </thead>
            <tbody>
              {#each data.identities as identity (identity.id)}
                <tr class="border-t border-border align-middle" data-testid={`identity-row-${identity.id}`}>
                  <td class="min-w-0 break-all py-2.5 pr-4 font-medium text-foreground">{identity.provider}</td>
                  <td class="min-w-0 break-all py-2.5 pr-4 text-muted-foreground">{identity.email || '—'}</td>
                  <td class="whitespace-nowrap py-2.5 pr-4 text-muted-foreground">{formatLinkedAt(identity.linked_at)}</td>
                  <td class="whitespace-nowrap py-2.5 text-right">
                    <Button
                      testId={`identity-unlink-${identity.id}`}
                      variant="danger-outline"
                      size="sm"
                      disabled={lastCredential}
                      loading={unlinkingId === identity.id}
                      onclick={() => unlink(identity)}
                    >
                      {$t('settings.identity.unlink')}
                    </Button>
                  </td>
                </tr>
              {/each}
            </tbody>
          </table>
        </div>
      {/if}

      {#if lastCredential}
        <p data-testid="identity-last-credential" class="mt-3 text-[13px] text-muted-foreground">
          {$t('settings.identity.last_credential')}
        </p>
      {/if}

      {#if !data.oidc_enabled}
        <p data-testid="identity-oidc-disabled" class="mt-3 text-[13px] text-muted-foreground">
          {$t('settings.identity.oidc_disabled')}
        </p>
      {/if}

      {#if data.oidc_enabled}
        <div class="mt-4">
          <Button testId="identity-bind" variant="primary" size="lg" loading={binding} onclick={() => bind()}>
            {$t('settings.identity.bind')}
          </Button>
        </div>
      {/if}
    {/if}
  </SettingsSection>
{/if}
