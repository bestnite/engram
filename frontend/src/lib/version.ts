/**
 * 程序版本。
 *
 * 服务端在装配期把版本号注入 SPA 入口 `index.html` 的 <head>：
 * `<meta name="engram-version" content="v0.1.4">`。值来自构建时注入的标签
 * （`-ldflags "-X main.version=<tag>"`）；未注入的本地构建是 `dev (<提交短哈希>)`。
 * 开发模式（vite dev）或静态预览下没有这枚 meta，返回空串，页脚随之不显示版本。
 */

const META_NAME = 'engram-version';

/** appVersion 读取服务端注入的程序版本；缺失时返回空串。 */
export function appVersion(): string {
  if (typeof document === 'undefined') return '';
  const meta = document.querySelector(`meta[name="${META_NAME}"]`);
  return (meta?.getAttribute('content') ?? '').trim();
}
