/**
 * SPA 端 MathJax 加载与排版。
 *
 * MathJax 3 是自托管静态资源（`internal/web/static/js/mathjax/tex-svg.js`，go:embed 进
 * 二进制，无公网 CDN）。它的内容哈希 URL 在 Vite 构建期未知，因此 Go 端在 SPA 入口
 * `index.html` 的 <head> 注入 `<meta name="engram-mathjax" content="…">` 把 URL 交给前端；
 * 本模块读取该 meta，用动态 `<script src>` 引入。CSP 的 `script-src 'self'` 已放行同源
 * 外链脚本，所以既不需要内联配置脚本，也不需要 nonce / 'unsafe-inline'。
 *
 * 排版边界：只对「服务端清洗后返回的 HTML 容器」调用 typeset，绝不整页排版——编辑器里的
 * 原始 Markdown 是纯文本，绝不能经 HTML sink 或 MathJax 二次解释。
 */

const META_NAME = 'engram-mathjax';

/** MathJax 全局对象中本模块用到的成员。 */
interface MathJaxGlobal {
  typesetPromise?: (elements?: Element[]) => Promise<void>;
  typesetClear?: (elements?: Element[]) => void;
}

/**
 * MathJax v3 启动配置。`startup.typeset: false` 关闭整页自动排版：SPA 的内容是异步到达的，
 * 只应在服务端清洗后的节点上显式排版。分隔符沿用打包版本的默认值——行内 `\(…\)`、块级
 * `\[…\]`（并保留 `$$…$$`），与 SSR 页面行为一致。
 */
const MATHJAX_CONFIG = {
  tex: {
    inlineMath: [['\\(', '\\)']],
    displayMath: [['$$', '$$'], ['\\[', '\\]']],
  },
  startup: { typeset: false },
};

let loadPromise: Promise<MathJaxGlobal | null> | null = null;

function currentDocument(): Document | null {
  return typeof document === 'undefined' ? null : document;
}

/**
 * mathjaxURL 读取服务端注入的 MathJax 哈希 URL。缺失（开发模式、资源未嵌入）返回空串，
 * 调用方据此跳过加载，与 SSR 缺资源时不引用 MathJax 的行为一致。
 */
export function mathjaxURL(): string {
  const doc = currentDocument();
  if (!doc) return '';
  const meta = doc.querySelector(`meta[name="${META_NAME}"]`);
  const content = meta?.getAttribute('content') ?? '';
  return content.startsWith('/') ? content : '';
}

/**
 * ensureMathJax 幂等地注入自托管 MathJax 脚本并返回其全局对象。
 *
 * 返回 null 表示不加载（无 DOM、无 URL，或脚本加载失败）。重复调用共享同一次加载，
 * 不会插入第二个 <script>。
 */
export function ensureMathJax(): Promise<MathJaxGlobal | null> {
  if (loadPromise) return loadPromise;
  const doc = currentDocument();
  const url = mathjaxURL();
  if (!doc || !url) {
    loadPromise = Promise.resolve(null);
    return loadPromise;
  }
  const win = (doc.defaultView ?? (globalThis as unknown as Window)) as unknown as {
    MathJax?: MathJaxGlobal;
  };
  // 已有实例（例如脚本已由别处加载）直接复用，不重复注入。
  if (win.MathJax && typeof win.MathJax.typesetPromise === 'function') {
    loadPromise = Promise.resolve(win.MathJax);
    return loadPromise;
  }
  // MathJax v3 在脚本加载时读取 window.MathJax，故先写配置再注入脚本。
  win.MathJax = MATHJAX_CONFIG as unknown as MathJaxGlobal;
  loadPromise = new Promise((resolve) => {
    const script = doc.createElement('script');
    script.src = url;
    script.async = true;
    script.onload = () => resolve(win.MathJax ?? null);
    script.onerror = () => resolve(null);
    doc.head.appendChild(script);
  });
  return loadPromise;
}

/**
 * typeset 对给定的「服务端清洗后的 HTML」容器重新排版。
 *
 * 先 typesetClear 再 typesetPromise：清掉旧节点的 MathJax 排版状态，避免换卡/换答案后
 * 残留过期的公式 DOM（换卡路径）。渲染失败只吞掉——公式排版失败不该
 * 打断编辑页或复习页（与 SSR 的 notes.js / review.js 同策略）。
 */
export async function typeset(nodes: Array<Element | null | undefined>): Promise<void> {
  const targets = nodes.filter((node): node is Element => node != null);
  if (targets.length === 0) return;
  const mj = await ensureMathJax();
  if (!mj || typeof mj.typesetPromise !== 'function') return;
  try {
    mj.typesetClear?.(targets);
    await mj.typesetPromise(targets);
  } catch {
    // 排版失败不冒泡：页面其余交互照常。
  }
}
