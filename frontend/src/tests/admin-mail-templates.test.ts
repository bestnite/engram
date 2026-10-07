import { describe, it, expect, beforeEach } from 'vitest';
import { render } from 'svelte/server';
import AdminMailTemplatesView from '../lib/views/admin/AdminMailTemplatesView.svelte';
import { setLocale } from '../lib/i18n';
import { zhCN } from '../lib/i18n/locales/zh-CN';
import { en } from '../lib/i18n/locales/en';
import { matchRoute } from '../lib/router';
import { routes } from '../lib/router/routes';
import type { AdminMailTemplatesResponse } from '../lib/api';

// 邮件模板管理页（DESIGN.md §4.7）。夹具刻意只放两个变量：一个必填（链接）、一个可选，
// 因为「必填标记」正是这个页面要传达的核心信息。

const fixture: AdminMailTemplatesResponse = {
  locales: ['zh-CN', 'en'],
  site_default_locale: 'zh-CN',
  types: [
    {
      type: 'password_reset',
      class: 'security',
      label_key: 'mail.prefs.type.password_reset',
      vars: [
        { name: 'url', required: true, note_key: 'admin.mail.var.reset_url' },
        { name: 'site', required: false, note_key: 'admin.mail.var.site' },
      ],
    },
  ],
  rows: [],
};

describe('AdminMailTemplatesView', () => {
  beforeEach(() => {
    setLocale('zh-CN');
  });

  it('renders the type, the variable list and which variables are required', () => {
    const { html } = render(AdminMailTemplatesView, {
      props: { initialLoading: false, initialData: fixture },
    });
    expect(html).toContain('data-testid="admin-mail-template-type"');
    expect(html).toContain('data-testid="admin-mail-template-locale"');
    expect(html).toContain('data-testid="admin-mail-template-vars"');
    // 变量以 {{name}} 形式展示——管理员要照着它写模板。
    expect(html).toContain('{{url}}');
    expect(html).toContain('{{site}}');
    // 必填标记：这是页面最要紧的信息（缺链接的邮件发不出去）。
    expect(html).toContain('data-testid="admin-mail-template-var-required"');
    // 没有自定义模板时显示「内置」，而不是空白。
    expect(html).toContain('data-testid="admin-mail-template-state"');
    expect(html).toContain('内置');
  });

  it('localizes the variable notes instead of showing the raw catalog keys', () => {
    const { html } = render(AdminMailTemplatesView, {
      props: { initialLoading: false, initialData: fixture },
    });
    expect(html).not.toContain('admin.mail.var.reset_url');
    expect(html).toContain(zhCN['admin.mail.var.reset_url']);
  });

  it('renders a localized English page under the en locale', () => {
    setLocale('en');
    const { html } = render(AdminMailTemplatesView, {
      props: { initialLoading: false, initialData: fixture },
    });
    expect(html).toContain(en['admin.mail.heading']);
    expect(html).toContain(en['admin.mail.var.reset_url']);
  });

  it('shows the loading skeleton before data arrives', () => {
    const { html } = render(AdminMailTemplatesView, { props: { initialLoading: true } });
    expect(html).toContain('data-testid="admin-mail-templates-loading"');
    expect(html).not.toContain('data-testid="admin-mail-template-type"');
  });
});

describe('mail template admin route and catalog keys', () => {
  it('resolves /admin/mail-templates to its own route', () => {
    expect(matchRoute('/admin/mail-templates', routes).route?.name).toBe('admin-mail-templates');
  });

  // 这份清单镜像 internal/mail/vars.go 的变量表。Go 侧每条 VarSpec 都带一个 NoteKey，
  // 由这个页面本地化；键写错的表现是页面上直接显示键名，所以在这里钉住两侧一致。
  const variableNames = [
    'actor', 'deck', 'deck_url', 'due_count', 'expires', 'fail_reason', 'invite_url', 'inviter', 'ip',
    'job_id', 'job_kind', 'jobs_url', 'media_limit', 'media_used', 'pass_rate', 'preset', 'presets_url',
    'reason', 'reset_url', 'review_url', 'reviewed_count', 'role', 'settings_url', 'site', 'stats_url',
    'status', 'streak_days', 'time', 'unsubscribe_url', 'username', 'verify_url', 'what',
  ];

  it('defines every variable note key in both catalogs', () => {
    for (const name of variableNames) {
      const key = `admin.mail.var.${name}` as keyof typeof zhCN;
      expect(zhCN[key], `zh-CN missing ${key}`).toBeTruthy();
      expect(en[key], `en missing ${key}`).toBeTruthy();
    }
  });

  it('defines the page copy in both catalogs', () => {
    const keys = [
      'admin.mail.heading',
      'admin.mail.intro',
      'admin.mail.type_label',
      'admin.mail.locale_label',
      'admin.mail.subject_label',
      'admin.mail.body_label',
      'admin.mail.vars_heading',
      'admin.mail.var_required',
      'admin.mail.fallback_hint',
      'admin.mail.preview',
      'admin.mail.preview_heading',
      'admin.mail.save',
      'admin.mail.saved',
      'admin.mail.delete',
      'admin.mail.deleted',
      'admin.mail.test',
      'admin.mail.test_queued',
      'admin.mail.empty_body',
      'admin.mail.error.invalid_template',
      'admin.mail.error.mail_not_configured',
      'admin.nav.mail_templates',
    ] as const;
    for (const key of keys) {
      expect(zhCN[key], `zh-CN missing ${key}`).toBeTruthy();
      expect(en[key], `en missing ${key}`).toBeTruthy();
    }
  });
});
