<script lang="ts">
  import { t } from '../../i18n';
  import { apiClient, ApiClientError, getApiErrorMessageKey } from '../../api';
  import Button from '../ui/Button.svelte';
  import { toast } from '../ui/toast';
  import SettingsSection from '../ui/SettingsSection.svelte';

  /**
   * 修改密码分区（安全页）。自带状态与提交，不依赖所在页面的其它表单。
   * 服务端的稳定错误码映射成具体提示；未列出的码退回通用的 API 错误文案。
   */
  let oldPassword = $state('');
  let newPassword = $state('');
  let saving = $state(false);

  const errorKeys: Record<string, string> = {
    invalid_current_password: 'settings.password.current_wrong',
    password_rejected: 'settings.password.rejected',
    password_unchanged: 'settings.password.unchanged',
    password_unavailable: 'settings.password.unavailable',
    invalid_request: 'settings.password.invalid',
  };

  async function submit(event: Event): Promise<void> {
    event.preventDefault();
    saving = true;
    try {
      await apiClient.changePassword({ old_password: oldPassword, new_password: newPassword });
      oldPassword = '';
      newPassword = '';
      toast.success($t('settings.password.changed'));
    } catch (err) {
      const code = err instanceof ApiClientError ? err.code : '';
      toast.error($t(errorKeys[code] || getApiErrorMessageKey(err)));
    } finally {
      saving = false;
    }
  }
</script>

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
