import { describe, it, expect } from 'vitest';
import * as fs from 'node:fs';
import * as path from 'node:path';
import { fileURLToPath } from 'node:url';

describe('Zero third-party runtime network dependencies', () => {
  it('ensures index.html contains no external CDN, script, or stylesheet links', () => {
    const indexPath = fileURLToPath(new URL('../../index.html', import.meta.url));
    const content = fs.readFileSync(indexPath, 'utf-8');

    // 检查是否有外部 http/https 脚本、样式表或字体引用
    const externalUrlRegex = /(?:href|src)=["'](https?:\/\/[^"']+)["']/gi;
    const matches: string[] = [];
    let match;
    while ((match = externalUrlRegex.exec(content)) !== null) {
      if (match[1]) {
        matches.push(match[1]);
      }
    }

    expect(matches).toEqual([]);
  });

  it('ensures app.css contains no external @import or remote url()', () => {
    const cssPath = fileURLToPath(new URL('../app.css', import.meta.url));
    const content = fs.readFileSync(cssPath, 'utf-8');

    // 检查是否有远程 url(...)
    const remoteUrlRegex = /url\(\s*["']?https?:\/\/[^"')]+["']?\s*\)/gi;
    expect(remoteUrlRegex.test(content)).toBe(false);

    // 检查是否有远程 @import
    const remoteImportRegex = /@import\s+["']https?:\/\/[^"']+["']/gi;
    expect(remoteImportRegex.test(content)).toBe(false);
  });

  it('ensures production build dist files contain no external CDN or third-party runtime URLs', () => {
    const distDir = fileURLToPath(new URL('../../dist', import.meta.url));
    if (!fs.existsSync(distDir)) {
      return;
    }

    const assetsDir = path.join(distDir, 'assets');
    if (!fs.existsSync(assetsDir)) {
      return;
    }

    const files = fs.readdirSync(assetsDir);
    for (const file of files) {
      const content = fs.readFileSync(path.join(assetsDir, file), 'utf-8');
      expect(content).not.toMatch(/https?:\/\/(?:cdn|cdnjs|unpkg|fonts\.googleapis|ajax\.googleapis)/i);
    }
  });
});
