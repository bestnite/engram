<script lang="ts">
  import { t } from '../i18n';
  import Page from '../components/ui/Page.svelte';
  import PageHeader from '../components/ui/PageHeader.svelte';
  import PasswordSection from '../components/settings/PasswordSection.svelte';
  import SettingsNav from './SettingsNav.svelte';
  import TOTPView from './TOTPView.svelte';

  /**
   * Security page (/settings/security): settings related to login credentials live together — password change first, then two-factor auth.
   * The two parts each read and write their own endpoint and do not depend on each other.
   */
</script>

<Page testId="security-view">
  <SettingsNav />
  <PageHeader title={$t('settings.security.heading')} testId="security-title" description={$t('settings.security.intro')} />
  <PasswordSection />
  <TOTPView />
</Page>
