import { describe, it, expect } from 'vitest';
import { zhCN } from '../lib/i18n/locales/zh-CN';
import { en } from '../lib/i18n/locales/en';

/**
 * /settings 与页头语言开关的文案内容。
 *
 * 两套语言包的键集合一致性由 parity.test.ts 统一保证，这里只管内容：必须存在的键在，
 * 且不得再教用户使用已经删掉的状态（切点的「未设置」）。
 */
describe('settings and header copy in both catalogs', () => {
  it('has no unset option in either catalog', () => {
    expect(zhCN['settings.profile.cutoff_default']).toBeUndefined();
    expect(en['settings.profile.cutoff_default']).toBeUndefined();
    // 文案不得再教用户「留空」或「默认」——控件里已经没有这两种状态了。
    expect(zhCN['settings.profile.cutoff_hint']).not.toContain('留空');
    expect(zhCN['settings.profile.cutoff_hint']).not.toContain('默认');
    expect(en['settings.profile.cutoff_hint']).not.toContain('blank');
    expect(en['settings.profile.cutoff_hint']).not.toContain('Default');
  });

  it('has the save-failure notice in both catalogs', () => {
    expect(zhCN['language.save_failed']).toBeTruthy();
    expect(en['language.save_failed']).toBeTruthy();
  });
});
