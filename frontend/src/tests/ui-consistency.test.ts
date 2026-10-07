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

describe('buttons and user-visible copy', () => {
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
  const find = (file: string) => sources.find((s) => s.file === file)?.src ?? '';

  it('routes the deck toolbar and the sharing actions through ui/Button', () => {
    // 这些按钮曾经是手写类名，其中共享页三个漏了字号类，于是落到正文 16px，
    // 同一页上比工具条的 12px 大一圈——统一到组件后这类漂移不可能再出现。
    const toolbar = find('views/DeckDetailView.svelte');
    expect(toolbar).toContain('<Button variant="outline" onclick={() => showExportModal = true}>');
    expect(toolbar).toContain('testId="create-note-link"');

    const sharing = find('views/DeckSharingView.svelte');
    for (const id of ['sharing-grant', 'sharing-create-link', 'sharing-save-visibility']) {
      expect(sharing, `sharing action ${id}`).toContain(`testId="${id}"`);
    }
    // 手写的那三个类名串不得再回来。
    expect(sharing).not.toContain('px-4 py-2 text-white cursor-pointer');
    expect(sharing).not.toContain('dark:border-zinc-700 px-4 py-2 cursor-pointer');
  });

  it('keeps user-visible text and placeholders out of the markup', () => {
    // 用户可见文案一律走语言包（AGENTS.md §2.1）。中文注释是允许且要求的，
    // 所以先剥掉注释再扫：正文文本节点与用户可见属性里不得出现汉字。
    const cjk = /[\u4e00-\u9fff]/;
    const visibleAttr = /(placeholder|title|aria-label|alt)="([^"]*)"/g;
    const offenders: string[] = [];
    for (const { file, src } of sources) {
      const body = src
        .replace(/<!--[\s\S]*?-->/g, '')
        .replace(/\/\*[\s\S]*?\*\//g, '')
        .split('\n')
        .filter((line) => !line.trim().startsWith('//'))
        .join('\n');
      const textNodes = body.replace(/<[^>]*>/g, '\n').split('\n').filter((line) => line.trim() && cjk.test(line));
      const first = textNodes[0];
      if (first) offenders.push(`${file}: ${first.trim()}`);
      for (const m of body.matchAll(visibleAttr)) {
        const value = m[2] ?? '';
        if (cjk.test(value)) offenders.push(`${file}: ${m[1]}="${value}"`);
      }
    }
    expect(offenders).toEqual([]);
  });
});

describe('form field skin', () => {
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

  // 属性里可能出现箭头函数的 `>`，所以不能拿 /<input[^>]*>/ 取标签。
  const scanTags = (src: string, names: string[]): string[] => {
    const out: string[] = [];
    let i = 0;
    for (;;) {
      const rest = src.slice(i);
      const m = new RegExp(`<(${names.join('|')})\\b`).exec(rest);
      if (!m || m.index === undefined) break;
      const start = i + m.index;
      let j = start + m[0].length;
      let depth = 0;
      let quote: string | null = null;
      while (j < src.length) {
        const c = src[j];
        if (quote) {
          if (c === quote) quote = null;
        } else if (c === '"' || c === "'") quote = c;
        else if (c === '{') depth += 1;
        else if (c === '}') depth = Math.max(0, depth - 1);
        else if (c === '>' && depth === 0) break;
        j += 1;
      }
      out.push(src.slice(start, j + 1));
      i = j + 1;
    }
    return out;
  };

  const classOf = (tag: string): string => /class="([^"]*)"/.exec(tag)?.[1] ?? '';

  it('styles every field through the field-input utility with an explicit size and width', () => {
    // 皮肤只允许有一处定义；字号必须由调用点显式给出——否则元素会继承祖先字号，
    // 同一页里两个输入框一个大一个小（按钮区已经发生过同样的漂移）。
    const offenders: string[] = [];
    for (const { file, src } of sources) {
      for (const tag of scanTags(src, ['input', 'textarea'])) {
        const cls = classOf(tag);
        if (!/\bfield-input\b/.test(cls)) continue;
        if (!/\btext-(xs|sm|base)\b/.test(cls)) offenders.push(`${file}: 缺字号 — ${cls.slice(0, 50)}`);
        // 宽度同样必须显式给出：field-input 只写皮肤，不给宽度时元素会掉回固有宽度
        //（约 20 个字符），同一页的框就宽窄不一 —— 这是真实报障过的形态。
        if (!/\b(w-|max-w-|min-w-)/.test(cls)) offenders.push(`${file}: 缺宽度 — ${cls.slice(0, 50)}`);
      }
    }
    expect(offenders).toEqual([]);
  });

  it('keeps the radius and border skin off the raw field elements', () => {
    // 皮肤搬进 @utility 之后，字段上再出现 rounded-*/border 就是第二份实现。
    const offenders: string[] = [];
    for (const { file, src } of sources) {
      for (const tag of scanTags(src, ['input', 'textarea'])) {
        if (/type="(file|checkbox|radio|range)"/.test(tag)) continue;
        const cls = classOf(tag);
        if (!cls) continue;
        // 按 token 精确判定：`dark:focus:border-zinc-100` 里的 border 不算皮肤。
        const skin = cls
          .split(/\s+/)
          .filter((token) => /^(rounded(-[\w.]+)?|border|border-zinc-\d+|focus:border(-[\w-]+)?)$/.test(token));
        if (skin.length) offenders.push(`${file}: ${skin.join(' ')}`);
      }
    }
    expect(offenders).toEqual([]);
  });

  it('uses the shared utility widely enough to be the only path', () => {
    const users = sources.filter((s) => s.src.includes('field-input')).length;
    expect(users).toBeGreaterThanOrEqual(25);
  });

  it('keeps hand-written filled button styles out of the views', () => {
    // 填充型按钮一律走 ui/Button。带 {…} 的类名是调用点算出来的激活态（tab / 导航），
    // 那不属于「两套实现」，跳过。
    const offenders: string[] = [];
    for (const { file, src } of sources) {
      for (const tag of scanTags(src, ['button', 'a'])) {
        const cls = classOf(tag);
        if (!cls || cls.includes('{') || cls.includes('}')) continue;
        const tokens = cls.split(/\s+/);
        const filled = tokens.some((t) => /^bg-(zinc-9\d\d|blue-600|rose-600|emerald-600)$/.test(t));
        if (filled && tokens.some((t) => /^px-/.test(t))) offenders.push(`${file}: ${cls.slice(0, 60)}`);
      }
    }
    expect(offenders).toEqual([]);
  });
});
});
