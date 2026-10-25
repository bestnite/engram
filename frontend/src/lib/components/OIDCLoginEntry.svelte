<script lang="ts">
  import { onMount } from 'svelte';
  import { t } from '../i18n';
  import { apiClient } from '../api';
  import type { OIDCInfo } from '../api';

  // OIDC login entry component.
  //
  // The SSR login page renders the second login button only when the OIDC config is fully usable; the SPA login view cannot know the server config at build time,
  // so it probes GET /api/v1/auth/oidc on mount (read-only, callable before login) and renders the entry only when enabled is true.
  // start_url comes from the server, from the same source as the SSR login page button's href, so the two ends cannot drift.
  //
  // The optional props.initial lets server rendering/tests inject a known state to avoid a first-frame flash; in production the probe result drives it.
  // The optional props.labelKey lets a host page name the entry after what the same server flow does for its visitor: on the login page it is a sign-in,
  // on the registration page the first OIDC sign-in also creates the account.
  interface Props {
    initial?: OIDCInfo | null;
    labelKey?: string;
  }

  let { initial = null, labelKey = 'account.oidc.button' }: Props = $props();

  // svelte-ignore state_referenced_locally
  let info = $state<OIDCInfo | null>(initial);

  onMount(async () => {
    if (info) {
      return;
    }
    try {
      info = await apiClient.getOIDC();
    } catch {
      // A failed probe is the same as "entry unavailable": never show a button that does nothing when clicked.
      info = null;
    }
  });
</script>

{#if info?.enabled}
  <div class="mt-4" data-testid="oidc-entry">
    <a
      href={info.start_url}
      class="inline-flex w-full items-center justify-center rounded-xl border border-zinc-200 bg-white px-4 py-2.5 text-sm font-medium text-zinc-700 shadow-2xs hover:bg-zinc-50 hover:border-zinc-300 dark:border-zinc-700 dark:bg-zinc-800 dark:text-zinc-200 dark:hover:bg-zinc-700 transition-colors"
    >
      {$t(labelKey)}
    </a>
  </div>
{/if}
