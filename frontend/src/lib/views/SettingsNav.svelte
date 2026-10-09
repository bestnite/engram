<script lang="ts">
  import { t } from '../i18n';
  import TabNav from '../components/ui/TabNav.svelte';

  /**
   * 个人设置的标签栏：与管理面板同一种页面级标签。各设置项是独立页面（独立地址，可直接打开或收藏），
   * 标签栏让它们之间一步切换，不必先回到 /settings 再进入。
   */
  const items = $derived([
    { href: '/settings', label: $t('settings.nav.general'), testId: 'settings-nav-general' },
    { href: '/settings/security', label: $t('settings.nav.security'), testId: 'settings-nav-security' },
    { href: '/settings/email', label: $t('settings.nav.mail'), testId: 'settings-nav-email' },
    { href: '/settings/keys', label: $t('settings.nav.api'), testId: 'settings-nav-keys' },
  ]);
</script>

<!-- /settings 只在完全相同时高亮：它是其它设置页的前缀，按前缀匹配会让「通用」在每个子页都亮着。 -->
<TabNav testId="settings-nav" ariaLabel={$t('nav.settings')} items={items} exact={['/settings']} />
