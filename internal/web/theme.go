package web

// 本文件保存首屏主题引导的编译期常量。它是全站唯一的 inline <script>：
// 深色用户若等外链脚本执行，会先看到一帧白底（外链脚本与样式表都是独立请求，必然输给首帧）。
//
// themeBootstrapJS 是脚本正文（不含 <script> 标签），单独成常量是为了对它取 SHA-256 交给 CSP
// 白名单（security_headers.go 的 cspScriptHash）。内容不随请求变化，所以用 hash 而不是
// per-request nonce：不必把 nonce 穿透到每个渲染调用。
const themeBootstrapJS = `(function(){var s;try{s=localStorage.getItem("engram-theme")}catch(e){}var d=s==="dark"||(s!=="light"&&window.matchMedia&&window.matchMedia("(prefers-color-scheme: dark)").matches);var r=document.documentElement;r.classList.toggle("dark",d);r.style.colorScheme=d?"dark":"light";r.style.backgroundColor=d?"#09090b":"#f8fafc";var m=document.querySelector('meta[name="theme-color"]');if(m){m.setAttribute("content",d?"#09090b":"#ffffff")}})();`

// themeBootstrap 是自带 <script> 标签的完整片段，由服务端在装配期注入 SPA 入口的 <head>
// （spa.SetShell），早于任何样式表执行。这里只做「首帧之前必须成立」的最小集合——暗色类、
// color-scheme、画布底色与 theme-color，用内联 style 设底色，任何样式表都抢不到这个竞态。
// 颜色值与 /pwa.js 的 applyTheme 保持一致。
const themeBootstrap = "<script>" + themeBootstrapJS + "</script>"
