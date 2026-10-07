import { describe, it, expect } from 'vitest';
import { readFileSync, readdirSync } from 'node:fs';
import { fileURLToPath } from 'node:url';
import { join, relative } from 'node:path';
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

describe('interactive pieces are owned by the component library', () => {
  // DESIGN.md §8：交互件优先用成熟组件库，组件库能覆盖的不得自写第二份实现。
  const walk = (dir: string): string[] => {
    const out: string[] = [];
    for (const entry of readdirSync(dir, { withFileTypes: true })) {
      const full = join(dir, entry.name);
      if (entry.isDirectory()) out.push(...walk(full));
      else if (entry.name.endsWith('.svelte')) out.push(full);
    }
    return out;
  };
  const libDir = fileURLToPath(new URL('../lib', import.meta.url));
  const sources = walk(libDir).map((file) => ({ file: relative(libDir, file), src: readFileSync(file, 'utf8') }));

  it('keeps no native <select> anywhere in views or components', () => {
    const offenders = sources.filter((s) => s.src.includes('<select')).map((s) => s.file);
    expect(offenders).toEqual([]);
  });

  it('keeps no native checkbox or radio input in views', () => {
    // 复选框/单选组都能用组件库表达（三态、方向键、roving tabindex 都在库里），
    // 视图里再出现原生 input[type=checkbox|radio] 就是第二份实现。
    const offenders = sources
      .filter((s) => s.file.startsWith('views/') && /type="(checkbox|radio)"/.test(s.src))
      .map((s) => s.file);
    expect(offenders).toEqual([]);
  });

  it('keeps the modal overlay inside ui/Dialog.svelte only', () => {
    // 自写模态各自手搓遮罩/ESC/焦点，行为与样式都不一致；弹层只允许有一处实现。
    const offenders = sources
      .filter((s) => s.src.includes('fixed inset-0') && s.file !== 'components/ui/Dialog.svelte')
      .map((s) => s.file);
    expect(offenders).toEqual([]);
  });

  it('routes every modal-owning view through ui/Dialog', () => {
    for (const file of [
      'views/DecksView.svelte',
      'views/PresetsView.svelte',
      'views/DeckDetailView.svelte',
    ]) {
      expect(sources.find((s) => s.file === file)?.src, file).toContain('components/ui/Dialog.svelte');
    }
  });
});

describe('loading states and the admin tab bar', () => {
  // 与上一节同样的全目录扫描：这条守卫要覆盖每个视图，而不是一份手工维护的清单。
  const walk = (dir: string): string[] => {
    const out: string[] = [];
    for (const entry of readdirSync(dir, { withFileTypes: true })) {
      const full = join(dir, entry.name);
      if (entry.isDirectory()) out.push(...walk(full));
      else if (entry.name.endsWith('.svelte')) out.push(full);
    }
    return out;
  };
  const libDir = fileURLToPath(new URL('../lib', import.meta.url));
  const sources = walk(libDir).map((file) => ({ file: relative(libDir, file), src: readFileSync(file, 'utf8') }));

  it('renders every page-level loading state through the shared Skeleton component', () => {
    // 加载态用骨架屏而不是空白或「加载中…」文字；这条样式必须只有一份实现
    //（同一组类名在二十个视图里各写一遍，就是下一次改版必然漂移的地方）。
    const pages = sources.filter((s) => s.file.startsWith('views/') && s.src.includes('-loading'));
    expect(pages.length).toBeGreaterThanOrEqual(20);
    const offenders = pages.filter((s) => !s.src.includes('<Skeleton')).map((s) => s.file);
    expect(offenders).toEqual([]);
    // 自写的页面级加载块（裸 div + 文案）已清零，只允许组件内部出现 data-testid。
    const raw = sources
      .filter((s) => s.file.startsWith('views/') && /data-testid="[a-z-]*-loading"/.test(s.src))
      .map((s) => s.file);
    expect(raw).toEqual([]);
  });

  it('keeps the pulse placeholder in a single implementation', () => {
    const owners = sources.filter((s) => s.src.includes('skeleton-block')).map((s) => s.file);
    expect(owners).toEqual(['components/ui/Skeleton.svelte']);
  });
  it('lays the admin tabs out on a fixed grid so switching pages cannot reflow them', () => {
    // 各 tab 文字长短不同：按内容宽度排（flex-wrap）时换行点在窄容器里会变，
    // 切页时 tab 位置就跟着跳动（用户报障形态）。等宽网格让每个 tab 宽度固定。
    const nav = sources.find((s) => s.file === 'views/admin/AdminNav.svelte')?.src ?? '';
    expect(nav).toContain('grid grid-cols-2 gap-1.5 sm:grid-cols-3 lg:grid-cols-4');
    expect(nav).not.toContain('flex-wrap');
    expect(nav).toContain('block rounded-lg px-3 py-1.5 text-center');
  });
});
