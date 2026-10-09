<script lang="ts">
  import { t } from '../i18n';
  import Page from '../components/ui/Page.svelte';
  import PageHeader from '../components/ui/PageHeader.svelte';
  import PasswordSection from '../components/settings/PasswordSection.svelte';
  import SettingsNav from './SettingsNav.svelte';
  import TOTPView from './TOTPView.svelte';

  /**
   * 安全页（/settings/security）：登录凭据相关的设置放在一起——先是修改密码，再是两步验证。
   * 两部分各自读写自己的接口，互不依赖。
   */
</script>

<Page testId="security-view">
  <SettingsNav />
  <PageHeader title={$t('settings.security.heading')} testId="security-title" description={$t('settings.security.intro')} />
  <PasswordSection />
  <TOTPView />
</Page>
