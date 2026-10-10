import { describe, it, expect, beforeEach } from 'vitest';
import { render } from 'svelte/server';
import HelpTip from '../lib/components/ui/HelpTip.svelte';
import { setLocale } from '../lib/i18n';

/**
 * 服务端渲染下的 HelpTip：只渲染触发按钮，说明正文在打开前不出现在页面里，
 * 按钮的读屏名称来自语言包并带上被说明的指标名。
 */
describe('HelpTip (server render)', () => {
  beforeEach(() => {
    setLocale('zh-CN');
  });

  it('renders a closed button labelled from the catalog', () => {
    const { html } = render(HelpTip, {
      props: { label: '留存率', text: '到期复习时答对的比例。', testId: 'tip' },
    });

    expect(html).toContain('data-testid="tip"');
    expect(html).toContain('aria-label="留存率是什么意思？"');
    expect(html).toContain('aria-expanded="false"');
    expect(html).toMatch(/<button[^>]*type="button"/);
    expect(html).not.toContain('到期复习时答对的比例。');
  });

  it('uses the English label when the locale is en', () => {
    setLocale('en');
    const { html } = render(HelpTip, { props: { label: 'Retention', text: 'x' } });

    expect(html).toContain('aria-label="What does “Retention” mean?"');
  });
});
