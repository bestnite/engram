<script lang="ts">
  import { t } from '../../i18n';
  import { routeStore } from '../../router';

  // 管理面板自己的导航（DESIGN.md §8.4）。全部子页都已实现，因此每一项都是可用链接。
  const items = [
    { href: '/admin', key: 'admin.nav.dashboard' },
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

  // 精确匹配子页；/admin 是概览，只在恰好命中时点亮，避免在任何子页都高亮它。
  function isActive(href: string, current: string): boolean {
    return href === '/admin' ? current === '/admin' : current === href || current.startsWith(href + '/');
  }
</script>

<nav data-testid="admin-nav" aria-label={$t('admin.nav.heading')} class="space-y-2">
  <h2 class="text-xs font-semibold uppercase tracking-wider text-zinc-500 dark:text-zinc-400">{$t('admin.nav.heading')}</h2>
  <!-- 等宽网格而非按内容宽度排：各 tab 文字长短不同，按内容宽度排会在窄容器里换行、
       切页时换行点随各子页容器宽度变化而跳动（用户报障形态）。网格让每个 tab 宽度固定。 -->
  <ul class="grid grid-cols-2 gap-1.5 sm:grid-cols-3 lg:grid-cols-4">
    {#each items as item (item.href)}
      <li>
        <a
          href={item.href}
          data-testid="admin-nav-{item.key}"
          aria-current={isActive(item.href, $routeStore.path) ? 'page' : undefined}
          class="block rounded-lg px-3 py-1.5 text-center text-sm font-medium transition-colors {$routeStore.path === item.href || isActive(item.href, $routeStore.path)
            ? 'bg-zinc-900 text-white dark:bg-zinc-100 dark:text-zinc-900'
            : 'text-zinc-600 hover:text-zinc-900 hover:bg-zinc-100 dark:text-zinc-400 dark:hover:text-zinc-100 dark:hover:bg-zinc-900'}"
        >
          {$t(item.key)}
        </a>
      </li>
    {/each}
  </ul>
</nav>
