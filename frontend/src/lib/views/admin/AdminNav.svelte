<script lang="ts">
  import { t } from '../../i18n';
  import TabNav from '../../components/ui/TabNav.svelte';

  // 管理面板自己的导航。全部子页都已实现，因此每一项都是可用链接。
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

<!-- 管理面板的二级导航：下划线标签栏（TabNav）。不再用网格按钮——网格三行高，
     选中项的实心块比页面标题还抢眼。 -->
<TabNav
  testId="admin-nav"
  ariaLabel={$t('admin.nav.heading')}
  items={items.map((item) => ({ href: item.href, label: $t(item.key), testId: `admin-nav-${item.key}` }))}
/>
