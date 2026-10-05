# 移动端冒烟清单（Mobile smoke checklist）

> 任务：ROADMAP.md **M8-6**。覆盖 **复习 / 编辑 / 离线提示 / 从主屏启动** 四条路径。
> 日期：**2026-10-02**（本清单的编写与代码侧核实日期；真机执行后请在文末「真机执行记录」补日期）。
> 规范来源：`DESIGN.md` §8.2（复习页交互）、§8.5（PWA）、§8.3（i18n）；`ROADMAP.md` M8-1、M8-2、M8-6。
>
> **诚实边界（先读）**
> 本清单的「实际观察」列有两种取值，含义严格区分：
> - **已核实**：能在仓库源码 / 静态资源 / 已运行的测试里直接读出的结论，附证据（文件行、命令输出）。
>   这类项**不得**当作真机通过。
> - **待 nite 真机执行**：必须在真实手机浏览器上手动操作才能得出的结论（触感、系统手势抑制、离线表现、主屏启动外观）。
>   本清单**不预填**此类结果；执行后由 nite 手写观察值。**任何一项在没有真机执行前都不得标记为通过。**

---

## 1. 前置条件

| 项 | 要求 |
|---|---|
| 访问地址 | `http://localhost:8080`（本机开发）或 `https://engram.example.com`（部署，**必须 https**）。 |
| 安全上下文 | Service Worker 只在**安全上下文**（`https://` 或 `localhost`）注册。用 `http://` 的局域网地址访问时 SW 不会注册，「从主屏启动」会退化为普通书签页 —— 真机冒烟**务必用 https 或 localhost**。 |
| 账号 | 一个可登录的普通用户账号（`/review`、`/decks/:id/notes/:nid` 都需要登录，未登录会 302 到 `/login`）。 |
| 数据 | 目标卡组里至少有 1 张 `basic` 卡的到期/新卡（否则复习页显示「当前没有到期的卡片。」）。 |
| 浏览器 | Android Chrome（最新）与 iOS Safari（最新）各一台；本文中「手机」指这两者。 |
| 建议分辨率 | 375×667 或更宽的竖屏；桌面 DevTools 设备模拟**不能**替代真机（系统手势、主屏安装、离线均为真机行为）。 |

服务端启动（开发用 SQLite 即可，摘自 `AGENTS.md` §4）：

```bash
DB_DRIVER=sqlite DB_DSN=data/engram.db AUTO_MIGRATE=1 go run ./cmd/engram serve
```

---

## 2. 如何在真机上跑这份清单（一句话）

**用手机浏览器打开 `http://localhost:8080/`（或你的 https 部署地址）的首页 —— manifest 与 service worker 都是公开路由，登录前就能「添加到主屏幕」；Android Chrome 用右上角 ⋮ →「添加到主屏幕 / 安装应用」，iOS Safari 用底部「分享」→「添加到主屏幕」—— 然后按下面 A→B→C→D 四节的步骤逐条操作，并在「真机执行记录」里写下每条的观察值。**

---

## 3. A. 复习（/review?deck=:id）

| # | 步骤 | 预期 | 实际观察 |
|---|---|---|---|
| A1 | 登录后打开 `/review?deck=<id>` | 页面正常渲染，顶部显示「今日剩余 N 张」「本次已复习 M」 | **已核实**：路由 `reviewPagePath = "/review"`（`internal/web/review.go:27`）；`requireUser` 未登录会 302 到 `/login`。页头计数元素为 `#review-remaining` / `#review-done`（`internal/web/views/review.templ:23-24`）。 |
| A2 | 点「显示答案」 | 答案区展开，四档评分按钮出现 | **待 nite 真机执行**（逻辑本身已在脚本化序列中跑通，见 §6）。 |
| A3 | 未揭示答案时，在卡片正文上横向滑动 | 任一横向滑动 = 显示答案 | **待 nite 真机执行**。脚本侧行为已核实（`review.js` `onTouchEnd`：`\|dx\| ≥ 45` 且 `\|dx\| ≥ \|dy\|` 才触发）。 |
| A4 | 已揭示后左滑 / 右滑 | 左滑 = 重来（Again），右滑 = 良好（Good） | **待 nite 真机执行**。脚本侧已核实（`clickRating("1")` / `clickRating("3")`）。 |
| A5 | 在作答输入框（`typed`/`numeric` 题型）内横向拖动 | 不触发评分，输入不受影响 | **待 nite 真机执行**。脚本侧已核实：落点在 `INPUT/TEXTAREA/SELECT/BUTTON/A` 上的滑动被忽略（`review.js` `interactiveTarget`）。 |
| A6 | 连续快速点两次评分按钮 | 页面**不缩放** | **待 nite 真机执行**（iOS Safari / Android Chrome 的系统双击缩放是否被抑制，无法在 CI 断言；`review_touch_test.go:16-22` 明确把这条列为手测项）。 |
| A7 | 在卡片正文上长按约 1 秒 | 不弹「拷贝 / 查询」菜单，不选中文字 | **待 nite 真机执行**。 |
| A8 | 观察评分按钮的点击区高度 | 每档按钮点击区 ≥ 44px | **已核实**：评分按钮 class 含 `min-h-11`（`review.templ:96`），Tailwind 默认 `min-h-11` = 2.75rem = **44px**（根字号 16px 时）；「显示答案」按钮同为 `min-h-11`（`review.templ:69`）。复习区域带 `touch-manipulation select-none`（`review.templ:21`）。 |
| A9 | 连刷 50 张卡 | 无卡顿、无系统菜单误触（`DESIGN.md` §12 M8 验收） | **待 nite 真机执行**。 |
| A10 | 桌面浏览器按空格显示答案、按 `1`–`4` 评分、`u` 撤销、`e` 编辑、`s` 暂停、`b` 埋藏 | 快捷键按文档生效 | **已核实**（`review.js` `onKeyDown`：空格/Enter、`1`–`4`、`u`/`s`/`b`/`e`；`review.templ:113` 渲染提示文案 `review.shortcuts`）。手机外接键盘同源，但真机手感仍需手测。 |

---

## 4. B. 编辑（/decks/:id/notes/:nid）

| # | 步骤 | 预期 | 实际观察 |
|---|---|---|---|
| B1 | 复习页点「编辑」（或按 `e`） | 跳到该 note 的编辑页 | **已核实**：复习页 `data-edit-href` = `/decks/<deckID>/notes/<noteID>`（`internal/web/review.go:312,385`）；编辑路由 `router.GET("/decks/:id/notes/:nid", s.noteEdit)`（`internal/web/notes.go:54`）。 |
| B2 | 打开编辑页 | 页面渲染出各字段的编辑控件与「保存」按钮 | **已核实**：`NoteEditPage` 对每个字段渲染 `<textarea>`/控件，字段名 `field.<name>`，保存按钮文案来自 `data.SaveLabel`（`internal/web/views/notes.templ:115-136`）。需登录 + 对该卡组有读写权限。 |
| B3 | 在字段里改文字 | 输入后约 300ms，右侧预览（`#note-preview`）局部刷新，不整页跳转 | **已核实**：字段控件带 `hx-post={data.PreviewURL} hx-trigger="keyup changed delay:300ms, change" hx-target="#note-preview"`（`notes.templ:132,212,214,218`）。 |
| B4 | 点「保存」 | 保存成功并返回卡片列表/详情，改动持久 | **待 nite 真机执行**（提交路径存在：表单 `method="post"` → `noteUpdate`，含 CSRF；但真机键盘弹起时的可用性需手测）。 |
| B5 | 手机上编辑时键盘弹起 | 输入框不被键盘遮挡，预览可滚动查看 | **待 nite 真机执行**。 |
| B6 | 中文输入法（拼音/手写）在 textarea 里输入 | 输入正常，不因 `select-none` 之类样式而无法选词 | **待 nite 真机执行**。注意：`select-none` 只加在**复习区域**（`#review-area`），编辑页未加，但需真机确认 IME 体验。 |

---

## 5. C. 离线提示

| # | 步骤 | 预期 | 实际观察 |
|---|---|---|---|
| C1 | 先在**在线**状态打开 `/review` 并显示答案 | 页面正常，`#review-offline` 横幅隐藏 | **已核实**：横幅为 `<p id="review-offline" class="... hidden" role="alert">`，初始隐藏（`review.templ:26`）。 |
| C2 | 保持页面**不刷新**，开启飞行模式 / 断网，然后点一档评分 | 页面顶部弹出红色横幅，文案为「网络已断开，上次评分未保存。」/「Connection lost. Your last rating was not saved.」，**不静默失败** | **已核实（文案与触发条件）**：文案来自语言包 `review.error_network`（`internal/i18n/locales/zh-CN.yaml:289`、`en.yaml:289`）；由 `review.js` 监听 htmx 的 `htmx:sendError` 时移除 `hidden` 显示（`review.js` `showOffline`）。**真机实际观感待 nite 执行。** |
| C3 | 在**离线**状态直接刷新 `/review` | 浏览器显示自身的「无法连接」错误页 | **已核实（按设计行为）**：Service Worker 只 cache-first 静态外壳，`isStaticShell` 仅命中 `/static/` 前缀与 `/manifest.webmanifest`；页面 HTML、`/api/`、`/review` 一律走网络（`internal/web/pwa.go:231-243`、`serviceWorkerBody`）。即离线时**整页不可用**，横幅只在「页面已加载、单次评分请求失败」时出现。**真机表现待 nite 执行。** |
| C4 | 断网后恢复网络，重新评分 | 评分正常提交 | **待 nite 真机执行**。 |
| C5 | 确认其余页面（首页、卡组、编辑页）断网时的表现 | 无自定义离线横幅（只有复习页挂了 `review.js`） | **已核实**：`review.js` 仅在复习页通过 `data.JSURL` 引入（`review.templ:8-10`）；`base.templ` 全站只引 `/pwa.js`（只做 SW 注册，无离线横幅）。 |

---

## 6. D. 从主屏启动（PWA）

| # | 步骤 | 预期 | 实际观察 |
|---|---|---|---|
| D1 | 手机打开 `http://localhost:8080/`（或 https 部署地址） | manifest 与 service worker 未登录即可取到 | **已核实**：`registerPWARoutes` 为公开路由，`TestManifestAndServiceWorkerAreReachableWithoutSession` 通过（匿名 GET `/manifest.webmanifest`、`/sw.js`、`/pwa.js` 均 200）。 |
| D2 | 用浏览器菜单「添加到主屏幕 / 安装应用」 | 图标出现在主屏 | **待 nite 真机执行**。 |
| D3 | 从主屏图标启动 | 以**独立窗口**（无浏览器地址栏）打开，`start_url` 为 `/` | **已核实（manifest 字段）**：`Display: "standalone"`、`StartURL: "/"`、`Scope: "/"`（`internal/web/pwa.go:81-90`）；`TestManifestIsStandaloneAndUsesHashedIcon` 通过。**实际启动外观待 nite 真机执行。** |
| D4 | 观察启动后的应用名 | 取自 `settings` 表的 `site.name`；未设置时回退语言包 `app.name`（zh/en 均为 `Engram`） | **已核实**：`pwaIdentity`（`pwa.go:108-127`）；`TestManifestNamePrefersSettings` 通过；语言包 `internal/i18n/locales/{zh-CN,en}.yaml:3-4`。 |
| D5 | 观察图标 | 显示 Engram 图标 | **已核实（声明层面）**：manifest 只有 **1 个** 图标：`icons/icon.svg`，`sizes: "any"`，`type: "image/svg+xml"`，`purpose: "any maskable"`，src 为内容哈希路径 `/static/v/<hash>/icons/icon.svg`（`pwa.go:91-98`；`internal/web/static/icons/icon.svg` 为 512×512 viewBox 的纯 SVG）。**没有 192×192 / 512×512 的 PNG 图标** —— 见 §7 已知限制。真机安装后图标是否正常显示待 nite 执行。 |
| D6 | 观察启动画面 / 状态栏颜色 | 背景 `#f8fafc`、主题色 `#2563eb` | **已核实（声明层面）**：`BackgroundColor "#f8fafc"`、`ThemeColor "#2563eb"`（`pwa.go:88-89`）；`base.templ:10` 亦有 `<meta name="theme-color" content="#2563eb"/>`。真机观感待 nite 执行。 |
| D7 | 在主屏启动的应用里完成一次复习 | 功能与浏览器内一致 | **待 nite 真机执行**。 |
| D8 | iOS（Safari）重复 D2–D3 | 从主屏独立启动 | **待 nite 真机执行**。注意：`base.templ` **没有** `apple-mobile-web-app-capable` / `apple-touch-icon` 等 iOS 专用标签，iOS 端主屏启动外观需重点确认（见 §7）。 |

---

## 7. 已知限制与风险（供真机执行时重点确认）

1. **图标只有 SVG，无 192/512 PNG**：`pwaShellAssets` 与 manifest 只声明 `icons/icon.svg`（`sizes: "any"`）。Chromium 的「可安装性」判定与桌面/Android 的启动图标质量通常期望 192×192 与 512×512 位图；SVG 图标在各平台的支持程度不一致。真机 D2/D5 请确认：**能否出现「安装应用」入口**、安装后图标是否清晰。
2. **iOS 专用标签缺失**：`base.templ` 无 `apple-touch-icon`、无 `apple-mobile-web-app-capable`、无 `apple-mobile-web-app-status-bar-style`。iOS Safari 对标准 manifest `display: standalone` 的支持版本有限，iOS 主屏启动可能仍带地址栏或复用页面截图作图标。D8 请明确记录。
3. **SW 只在安全上下文注册**：`http://` 非 localhost 地址不会注册 SW（`pwa.js` 静默 catch，不报错）。冒烟必须用 https 或 localhost。
4. **离线只提示、不答题**：按 `DESIGN.md` §1 非目标，本服务不做离线答题；离线时整页不可用，仅「已加载页面上的单次评分失败」会触发横幅（§5 C3）。
5. **双击缩放的抑制范围**：抑制逻辑绑定在 `#review-area` 内的 `dblclick`，且 `viewport` 未设 `user-scalable=no`（有意保留可达性）。复习区域外（页头/页脚）的双击缩放不受影响 —— 这符合 `DESIGN.md` §8.2 的范围，但真机 A6 需在复习区域内测。
6. **`min-h-11` = 44px 依赖根字号**：若用户浏览器把默认字号调大，实际高度会大于 44px；调小则可能小于 44px。A8 请以真机渲染高度为准。

---

## 8. 本轮已核实的证据（命令输出）

以下命令均在 worktree `/home/nite/dev/engram-wt/m8-checklist` 内、于 **2026-10-02** 执行。

**脚本化触屏序列（M8-1）**：用零依赖 DOM 替身在 Node 里回放「滑动揭示 → 左滑 Again → 长按无菜单 → 双击无缩放 → 输入框内滑动被忽略」：

```
$ node internal/web/testdata/review_touch_sim.mjs internal/web/static/js/review.js
ok   - swipe while hidden reveals the answer
ok   - swipe while hidden shows the ratings
ok   - swipe while hidden hides the show-answer button
ok   - swipe left after reveal rates Again once
ok   - swipe left does not rate other grades
ok   - long-press contextmenu inside review area is prevented
ok   - double-tap dblclick inside review area is prevented
ok   - swipe starting on an input is ignored

all touch-sequence assertions passed
exit=0
```

**PWA 外壳与复习页契约（M8-2 / M8-1）**：

```
$ go test ./internal/web/ -run 'TestManifestIsStandaloneAndUsesHashedIcon|TestServiceWorkerCachesOnlyStaticAssets|TestPWAScriptIsServable|TestManifestAndServiceWorkerAreReachableWithoutSession|TestReviewPageTouchTargets|TestReviewTouchSequenceRatesCard|TestReviewScriptBindsTouchAndBlocksNativeGestures' -v
--- PASS: TestServiceWorkerCachesOnlyStaticAssets (0.23s)
--- PASS: TestManifestIsStandaloneAndUsesHashedIcon (0.28s)
--- PASS: TestPWAScriptIsServable (0.23s)
--- PASS: TestManifestAndServiceWorkerAreReachableWithoutSession (0.23s)
--- PASS: TestReviewScriptBindsTouchAndBlocksNativeGestures (0.00s)
--- PASS: TestReviewTouchSequenceRatesCard (0.03s)
--- PASS: TestReviewPageTouchTargets (0.25s)
PASS
ok  	git.nite07.com/nite/engram/internal/web	1.271s
```

> 说明：上述三项证明的是**代码/脚本层**的行为。真机项（A2–A7、A9、B4–B6、C2–C4、D2–D8）**不在**这些证据的覆盖范围内，必须由 nite 在真机上执行后填写。

---

## 9. 真机执行记录（由 nite 填写）

| 日期 | 设备 / 浏览器 | 地址 | 执行的项 | 观察结论 / 异常 |
|---|---|---|---|---|
| 待填 | 待填 | 待填 | 待填 | 待填 |

执行完请把本节每一行的「观察结论」写成可直接引用的句子（含截图路径更佳），并把本文档顶部的日期保持为「编写与代码侧核实日期」，另在此表记录真机执行日期。