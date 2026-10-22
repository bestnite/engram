<script lang="ts">
  import { t } from '../i18n';
  import TabNav from '../components/ui/TabNav.svelte';

  /**
   * Personal settings tab bar: the same kind of page-level tabs as the admin panel. Each settings item is a standalone page
   * (its own address, directly openable or bookmarkable), and the tab bar switches between them in one step, without going
   * back to /settings first.
   */
  const items = $derived([
    { href: '/settings', label: $t('settings.nav.general'), testId: 'settings-nav-general' },
    { href: '/settings/security', label: $t('settings.nav.security'), testId: 'settings-nav-security' },
    { href: '/settings/email', label: $t('settings.nav.mail'), testId: 'settings-nav-email' },
    { href: '/settings/keys', label: $t('settings.nav.api'), testId: 'settings-nav-keys' },
  ]);
</script>

<!-- /settings is highlighted only on an exact match: it is a prefix of the other settings pages, and prefix matching would keep "general" lit on every subpage. -->
<TabNav testId="settings-nav" ariaLabel={$t('nav.settings')} items={items} exact={['/settings']} />
