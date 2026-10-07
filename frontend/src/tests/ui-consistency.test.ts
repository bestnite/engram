import { describe, it, expect } from 'vitest';
import { readFileSync } from 'node:fs';
import { fileURLToPath } from 'node:url';
import { zhCN } from '../lib/i18n/locales/zh-CN';
import { en } from '../lib/i18n/locales/en';

const read = (rel: string) => readFileSync(fileURLToPath(new URL(rel, import.meta.url)), 'utf-8');

describe('APIKeysView revoke wiring', () => {
  const view = read('../lib/views/APIKeysView.svelte');

  it('reloads the list from the server instead of only dropping the row locally', () => {
    // 服务端 ListByUser 返回含已撤销的 key（带 revoked_at）。只做本地 filter 的话，
    // 撤销结果只在本会话成立，刷新后已撤销项会「又出现」——这正是被报告的缺陷。
    expect(view).toContain('await load(true)');
    expect(view).not.toContain('key.id !== key.id');
    expect(view).not.toContain('item.id !== key.id');
  });

  it('renders revoked keys as a distinct, non-actionable group', () => {
    // 分组依据是服务端给的 revoked_at，而不是前端自己记的状态。
    expect(view).toContain('k.revoked_at');
    expect(view).toContain('data-testid="keys-revoked-badge-{key.id}"');
    expect(view).toContain("$t('keys.revoked')");
    // 活跃项才有撤销按钮。
    expect(view).toContain('data-testid="keys-revoke-{key.id}"');
  });

  it('has both new catalog keys in both languages', () => {
    for (const key of ['keys.revoking', 'keys.revoked'] as const) {
      expect(zhCN[key], `zh-CN missing ${key}`).toBeTruthy();
      expect(en[key], `en missing ${key}`).toBeTruthy();
    }
  });
});

describe('page skeleton consistency', () => {
  // 全站应用页统一 max-w-4xl；登录/注册等认证页是独立的一类（窄卡片），不在此约定内。
  const appPages: Array<[string, string]> = [
    ['../lib/views/HomeView.svelte', 'py-10 max-w-4xl'],
    ['../lib/views/DecksView.svelte', 'py-10 max-w-4xl'],
    ['../lib/views/SettingsView.svelte', 'py-10 max-w-4xl'],
    ['../lib/views/APIKeysView.svelte', 'py-10 max-w-4xl'],
    ['../lib/views/StatsView.svelte', 'py-10 max-w-4xl'],
    ['../lib/views/DeckDetailView.svelte', 'py-10 max-w-4xl'],
    ['../lib/views/ReviewView.svelte', 'max-w-4xl'],
    ['../lib/views/PresetsView.svelte', 'max-w-4xl'],
    ['../lib/views/NoteEditView.svelte', 'py-10 max-w-4xl'],
    ['../lib/views/NoteCreateView.svelte', 'py-10 max-w-4xl'],
    ['../lib/views/ImportView.svelte', 'py-10 max-w-4xl'],
    ['../lib/views/PresetsView.svelte', 'max-w-4xl'],
    ['../lib/views/NotificationPrefsView.svelte', 'max-w-4xl'],
    ['../lib/views/TOTPView.svelte', 'max-w-4xl'],
    ['../lib/views/DeckSettingsView.svelte', 'max-w-4xl'],
    ['../lib/views/DeckSharingView.svelte', 'max-w-4xl'],
  ];

  for (const [file, expected] of appPages) {
    it(`${file} uses the shared 4xl body width`, () => {
      expect(read(file)).toContain(expected);
    });
  }

  it('uses no grey card utility in views and components', () => {
    // card-subtle（灰底）与别处的 card-elevated（白底）混用是「卡片颜色不一致」的来源；
    // 面板底色统一由 card-elevated 表达。
    const offenders: string[] = [];
    for (const file of [
      ...appPages.map(([f]) => f),
      '../lib/views/DeckSharingView.svelte',
      '../lib/views/DeckSettingsView.svelte',
      '../lib/views/NotificationPrefsView.svelte',
      '../lib/views/TOTPView.svelte',
    ]) {
      if (read(file).includes('card-subtle')) offenders.push(file);
    }
    expect(offenders).toEqual([]);
  });
});

describe('no decorative arrow glyphs in user-facing copy', () => {
  it('keeps entity references out of page copy', () => {
    const files = [
      '../lib/views/HomeView.svelte',
      '../lib/views/DeckDetailView.svelte',
      '../lib/views/DeckSharingView.svelte',
      '../lib/views/DeckSettingsView.svelte',
      '../lib/views/NoteEditView.svelte',
      '../lib/views/NoteCreateView.svelte',
      '../lib/views/NotificationPrefsView.svelte',
      '../lib/views/TOTPView.svelte',
    ];
    for (const file of files) {
      const src = read(file);
      // 只检查会渲染出来的箭头实体与字符；备注里的「→」不算（它们在 <script>/注释里，
      // 这里用「语言包插值紧邻箭头」的形态做判据，避免把说明文字误判成文案）。
      expect(src, `${file} renders &rarr;`).not.toContain('&rarr;');
      expect(src, `${file} renders &larr;`).not.toContain('&larr;');
      expect(src, `${file} renders a text arrow`).not.toMatch(/\{\$t\([^)]*\)\}\s*(←|→)/);
      expect(src, `${file} renders a leading text arrow`).not.toMatch(/(←|→)\s*\{\$t\(/);
    }
  });
});
