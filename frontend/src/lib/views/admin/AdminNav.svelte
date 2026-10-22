<script lang="ts">
  import { t } from '../../i18n';
  import TabNav from '../../components/ui/TabNav.svelte';

  // The admin panel's own navigation. Every subpage is implemented, so each item is a usable link.
  const items = [
    { href: '/admin/users', key: 'admin.nav.users' },
    { href: '/admin/registration', key: 'admin.nav.registration' },
    { href: '/admin/oidc', key: 'admin.nav.oidc' },
    { href: '/admin/smtp', key: 'admin.nav.smtp' },
    { href: '/admin/settings', key: 'admin.nav.settings' },
    { href: '/admin/jobs', key: 'admin.nav.jobs' },
    { href: '/admin/audit', key: 'admin.nav.audit' },
    { href: '/admin/health', key: 'admin.nav.health' },
    { href: '/admin/api-keys', key: 'admin.nav.api_keys' },
    { href: '/admin/i18n', key: 'admin.nav.i18n' },
  ];
</script>

<!-- The admin panel's secondary navigation: an underline tab bar (TabNav). No more grid buttons — the grid is three rows tall,
     and the selected item's solid block was more eye-catching than the page title. -->
<TabNav
  testId="admin-nav"
  ariaLabel={$t('admin.nav.heading')}
  items={items.map((item) => ({ href: item.href, label: $t(item.key), testId: `admin-nav-${item.key}` }))}
/>
