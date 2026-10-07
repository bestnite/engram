import { describe, it, expect } from 'vitest';
import { readFileSync } from 'node:fs';
import { fileURLToPath } from 'node:url';
import { zhCN } from '../lib/i18n/locales/zh-CN';
import { en } from '../lib/i18n/locales/en';

const read = (rel: string) => readFileSync(fileURLToPath(new URL(rel, import.meta.url)), 'utf-8');

/**
 * /settings 资料区的控件接线：时区走可搜索下拉、日切点走单选下拉。
 * 两处都是「控件库承担交互、调用方只给数据」的形态，断言钉在调用面上——
 * 弹层内容在 Portal 里、只在展开时渲染，按内部节点断言既脆弱又测不到真东西。
 */
describe('settings profile controls', () => {
  const view = read('../lib/views/SettingsView.svelte');

  it('renders the time zone through the searchable combobox', () => {
    expect(view).toContain('components/ui/Combobox.svelte');
    expect(view).toContain('testId="settings-timezone"');
    expect(view).toContain('options={timezoneChoices}');
    expect(view).toContain('const timezoneChoices = $derived(timezoneOptions(timezone));');
  });

  it('renders the review day cutoff as a select over the 24 hours, with no unset option', () => {
    expect(view).toContain('testId="settings-cutoff"');
    expect(view).toContain('options={cutoffChoices}');
    expect(view).toContain('Array.from({ length: 24 }');
    // 「未设置（NULL）」不是一个值得让用户选的状态：切点必须是具体整点。
    expect(view).not.toContain('cutoff_default');
    expect(view).not.toContain("'default'");
  });

  it('shows the server default for accounts that still have NULL stored', () => {
    // 旧账号（含新建账号）在库里是 NULL，生效值就是服务端默认 04:00；界面照实显示它，
    // 保存一次即成为显式值——两者生效结果相同，所以这不是行为变化。
    expect(view).toContain('const rawCutoff = dayCutoff;');
    expect(view).toContain('dayCutoff = String(profile.day_cutoff_hour ?? DEFAULT_DAY_CUTOFF_HOUR);');
    expect(view).toContain('let dayCutoff = $state(String(DEFAULT_DAY_CUTOFF_HOUR));');
  });

  it('has no unset option in either catalog', () => {
    expect(zhCN['settings.profile.cutoff_default']).toBeUndefined();
    expect(en['settings.profile.cutoff_default']).toBeUndefined();
    // 文案不得再教用户「留空」或「默认」——控件里已经没有这两种状态了。
    expect(zhCN['settings.profile.cutoff_hint']).not.toContain('留空');
    expect(zhCN['settings.profile.cutoff_hint']).not.toContain('默认');
    expect(en['settings.profile.cutoff_hint']).not.toContain('blank');
    expect(en['settings.profile.cutoff_hint']).not.toContain('Default');
  });
});

describe('header language entry', () => {
  it('renders the switcher only for signed-out visitors', () => {
    // 页头切换器此前对已登录用户也可见，切完只改本地 store、刷新即回退，
    // 与 /settings 落库的那一份打架（用户报障的形态）。
    const header = read('../lib/components/NavHeader.svelte');
    expect(header).toContain('{#if !$authStore.authenticated}');
    expect(header).toContain('<LanguageSwitcher />');
  });

  it('writes the anonymous choice into the address instead of only the local store', () => {
    const switcher = read('../lib/components/LanguageSwitcher.svelte');
    expect(switcher).toContain('setLanguageInURL(value)');
  });
});
