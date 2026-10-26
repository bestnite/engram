<script lang="ts">
  import { t } from '../../i18n';
  import { apiClient, ApiClientError, getApiErrorMessageKey } from '../../api';
  import { authStore, markPasswordSet } from '../../auth';
  import Button from '../ui/Button.svelte';
  import { toast } from '../ui/toast';
  import SettingsSection from '../ui/SettingsSection.svelte';

  /**
   * Password section (security page). Carries its own state and submission and does not depend on other forms on the page.
   * The server's stable error codes map to specific messages; unlisted codes fall back to the generic API error copy.
   *
   * An account created through OIDC has no password to change, so it gets the "set first password" variant: one field and no
   * old password, because there is nothing to prove. `has_password === undefined` only happens with an older payload and keeps
   * the change form, which the server rejects rather than silently accepting a missing old password.
   */
  let oldPassword = $state('');
  let newPassword = $state('');
  let saving = $state(false);

  const passwordless = $derived($authStore.user?.has_password === false);

  const errorKeys: Record<string, string> = {
    invalid_current_password: 'settings.password.current_wrong',
    password_rejected: 'settings.password.rejected',
    password_unchanged: 'settings.password.unchanged',
    password_unavailable: 'settings.password.unavailable',
    password_already_set: 'settings.password.already_set',
    invalid_request: 'settings.password.invalid',
  };

  async function submit(event: Event): Promise<void> {
    event.preventDefault();
    saving = true;
    try {
      if (passwordless) {
        await apiClient.changePassword({ new_password: newPassword });
        newPassword = '';
        markPasswordSet();
        toast.success($t('settings.password.set_done'));
      } else {
        await apiClient.changePassword({ old_password: oldPassword, new_password: newPassword });
        oldPassword = '';
        newPassword = '';
        toast.success($t('settings.password.changed'));
      }
    } catch (err) {
      const code = err instanceof ApiClientError ? err.code : '';
      // Another request set the password between this page's read and this write. The store still says "no password",
      // so without this the form would keep offering the first-password variant and fail the same way on every retry.
      if (code === 'password_already_set' && passwordless) {
        markPasswordSet();
      }
      toast.error($t(errorKeys[code] || getApiErrorMessageKey(err)));
    } finally {
      saving = false;
    }
  }
</script>

{#if passwordless}
  <SettingsSection
    title={$t('settings.password.set_heading')}
    description={$t('settings.password.set_intro')}
    testId="settings-password"
  >
    <form onsubmit={submit} class="space-y-5">
      <label class="block max-w-sm text-sm font-medium text-foreground">{$t('settings.password.new_label')}
        <input type="password" autocomplete="new-password" bind:value={newPassword} required class="field-input mt-1.5 w-full text-sm font-normal" />
      </label>
      <Button type="submit" loading={saving} variant="outline" size="lg">{$t('settings.password.set_submit')}</Button>
    </form>
  </SettingsSection>
{:else}
  <SettingsSection title={$t('settings.password.heading')} testId="settings-password">
    <form onsubmit={submit} class="space-y-5">
      <div class="grid max-w-2xl gap-5 sm:grid-cols-2">
        <label class="block text-sm font-medium text-foreground">{$t('settings.password.old_label')}
          <input type="password" autocomplete="current-password" bind:value={oldPassword} required class="field-input mt-1.5 w-full text-sm font-normal" />
        </label>
        <label class="block text-sm font-medium text-foreground">{$t('settings.password.new_label')}
          <input type="password" autocomplete="new-password" bind:value={newPassword} required class="field-input mt-1.5 w-full text-sm font-normal" />
        </label>
      </div>
      <Button type="submit" loading={saving} variant="outline" size="lg">{$t('settings.password.submit')}</Button>
    </form>
  </SettingsSection>
{/if}
