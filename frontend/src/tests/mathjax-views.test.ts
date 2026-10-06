import { describe, it, expect } from 'vitest';
import * as fs from 'node:fs';
import { fileURLToPath } from 'node:url';

/**
 * SPA 公式渲染的 HTML sink 回归守护（DESIGN.md §6.1、§11）。
 *
 * MathJax 只应排版「服务端清洗后返回的 HTML」：本用例把两个视图里的 {@html …} 表达式钉死
 * 成白名单，并断言渲染后调用了 MathJax 排版。若有人把编辑器原始 Markdown 或字段原文塞进
 * HTML sink，这里会先变红。
 */

function viewSource(name: string): string {
  return fs.readFileSync(fileURLToPath(new URL(`../lib/views/${name}`, import.meta.url)), 'utf-8');
}

function htmlSinks(src: string): string[] {
  return [...src.matchAll(/\{@html\s+([^}]+?)\s*\}/g)].map((match) => match[1]!.trim());
}

describe('SPA MathJax wiring stays inside server-sanitized HTML sinks', () => {
  it('NoteEditView sinks only the preview API HTML and typesets that container', () => {
    const src = viewSource('NoteEditView.svelte');
    // 只允许预览接口返回的 front_html / back_html；不得是编辑器字段原文。
    expect(htmlSinks(src)).toEqual(['card.front_html', 'card.back_html']);
    expect(src).toContain("import { typeset } from '../mathjax'");
    expect(src).toContain('await tick();');
    expect(src).toContain('await typeset([previewSection]);');
  });

  it('ReviewView sinks only server-returned answer HTML and typesets those containers', () => {
    const src = viewSource('ReviewView.svelte');
    // 判分反馈答案与揭示答案都来自服务端清洗后的 HTML；字段原文一律走纯文本。
    expect(htmlSinks(src)).toEqual(['feedback.answer_html', 'revealedAnswerHTML']);
    expect(src).toContain("import { typeset } from '../mathjax'");
    expect(src).toContain('await typeset([feedbackSection]);');
    expect(src).toContain('await typeset([revealedSection]);');
  });
});
