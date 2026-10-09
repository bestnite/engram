<script lang="ts">
  import { t } from '../../i18n';
  import { routeStore } from '../../router';

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

  // 子页本身或其下级路径都算命中。
  function isActive(href: string, current: string): boolean {
    return current === href || current.startsWith(href + '/');
  }
</script>

<!-- 管理面板的二级导航：与卡组详情页同一种下划线标签栏。一行排开，窄屏横向滚动，
     不再用网格按钮——网格三行高，选中项的实心块比页面标题还抢眼。 -->
<nav data-testid="admin-nav" aria-label={$t('admin.nav.heading')} class="mb-6 border-b border-border">
  <ul class="-mb-px flex gap-6 overflow-x-auto overflow-y-hidden">
    {#each items as item (item.href)}
      {@const active = isActive(item.href, $routeStore.path)}
      <li class="shrink-0">
        <a
          href={item.href}
          data-testid="admin-nav-{item.key}"
          aria-current={active ? 'page' : undefined}
          class="block whitespace-nowrap border-b-2 py-2.5 text-sm transition-colors {active
            ? 'border-foreground font-medium text-foreground'
            : 'border-transparent text-muted-foreground hover:text-foreground'}"
        >
          {$t(item.key)}
        </a>
      </li>
    {/each}
  </ul>
</nav>
