# 无障碍清单（Accessibility checklist）

> 任务：AGENTS.md §5 **M8-5**（keyboard-only flow for review and editing, visible focus, labels on inputs）。
> 覆盖页面：**复习页、卡组页、设置页**（外加卡片列表页，因为它是「编辑」流程的入口）。
> 日期：**2026-10-02**（本清单的编写与代码侧核实日期；真人手测后请在文末「手动执行记录」补日期）。
> 规范来源：`DESIGN.md` §8.2（复习页交互与快捷键）、§8.3（页面清单）、§8.4（管理面板）；`AGENTS.md` §4（Definition of done）、§5 M8-5。
>
> **诚实边界（先读）**
> 本清单没有真机、也没有真人参与，因此「实际观察」列只有两种取值，含义严格区分：
> - **已核实**：能在渲染出的 HTML、静态资源或已运行的测试里直接读出的结论，附证据（测试名、文件行、命令输出）。
>   这类项**不得**当作「人工键盘体验通过」。
> - **待 nite 手动执行**：必须在真实浏览器里用键盘/屏幕阅读器手动操作才能得出的结论（Tab 顺序手感、焦点环对比度、读屏读音、输入法）。
>   本清单**不预填**此类结果；执行后由 nite 手写观察值。**任何一项在手动执行前都不得标记为通过。**

---

## 1. 前置条件

| 项 | 要求 |
|---|---|
| 访问地址 | `http://localhost:8080`（本机开发）。 |
| 账号 | 一个可登录的普通用户（`/review`、`/decks/:id/notes`、`/settings` 都需要登录，未登录 302 到 `/login`）。 |
| 数据 | 一个含 `basic` 卡的卡组（普通复习页）；另建议一个含 `numeric`/`typed`/`choice_*` 卡的卡组（作答类复习页，含文本输入框）。 |
| 浏览器 | 桌面 Chrome / Firefox 各一台（键盘流程）；读屏可选 NVDA（Windows）或 VoiceOver（macOS）。 |
| 键盘 | 只允许 Tab / Shift+Tab / Enter / Space / 方向键 / 1–4 / u e s b，不得使用鼠标。 |

服务端启动（开发用 SQLite 即可，摘自 `AGENTS.md` §4）：

```bash
DB_DRIVER=sqlite DB_DSN=data/engram.db AUTO_MIGRATE=1 go run ./cmd/engram serve
```

---

## 2. 复习页（`/review?deck=:id`）

| # | 步骤 | 预期 | 实际观察 |
|---|---|---|---|
| R1 | 用 Tab 从页头进入复习区，逐个走过「显示答案」按钮、评分按钮、动作按钮（撤销/暂停/埋藏）、「编辑」链接 | 每个可聚焦控件都有可见焦点，Tab 顺序按视觉顺序（上→下、左→右） | **待 nite 手动执行**。逻辑侧已核实：`input.css` 有全局 `:focus-visible` 规则（见 R7）；各控件本身是原生 `button`/`a`，默认可聚焦。 |
| R2 | 按空格 / Enter | 显示答案，答案区与四档评分按钮出现 | **已核实（脚本绑定）**：`review.js` 的 `onKeyDown` 处理 `e.key === " " || e.key === "Enter"` 调 `revealAnswer()`；`TestAccessibilityReviewKeyboardShortcuts` 通过。**真实手感待 nite 手动执行。** |
| R3 | 揭示答案后按 `1`–`4` | 分别提交 重来/困难/良好/简单 | **已核实（脚本绑定）**：`review.js` 处理 `e.key === "1"`…，`clickRating(key)` 命中 `button[data-rating]`；`TestAccessibilityReviewKeyboardShortcuts` 通过。**真机键盘手感待 nite 手动执行。** |
| R4 | 四档评分按钮的**可读名称** | 每个评分按钮有可见文本，不是只有图标 | **已核实**：`TestAccessibilityRatingButtonsHaveReadableNames` 通过，读到 4 个不同的按钮文本（重来/困难/良好/简单）。 |
| R5 | 作答类卡片（`typed`/`numeric`）的答案输入框 | 输入框有与之关联的 `<label>`，不是只有 placeholder | **已核实**：`TestAccessibilityFormControlsHaveLabels` 断言 `id="review-answer-input"` 与 `for="review-answer-input"` 同时存在；标签文案来自语言包 `a11y.review.answer_input`（zh「你的答案」/ en「Your answer」）。 |
| R6 | 复习页所有可见表单控件（含隐藏字段以外的全部 input/select/textarea） | 每个都有 `<label>` / `for=` / `aria-label` 三者之一 | **已核实**：`TestAccessibilityFormControlsHaveLabels` 对 `review-basic` 审计 13 个控件、`review-graded` 审计 14 个控件，0 个未标注。 |
| R7 | 键盘焦点是否可见 | 焦点控件有清晰的高对比焦点环 | **已核实（样式来源）**：`internal/web/static/css/input.css` 有 `:where(a, button, input, select, textarea, summary, [tabindex]):focus-visible { outline: 2px solid #2563eb; outline-offset: 2px; }`；`go generate ./...` 后产物 `tailwind.css` 含该规则；`TestAccessibilityFocusVisibleStyle` 通过。**焦点环在真实页面上的对比度与可见度待 nite 手动执行。** |
| R8 | 键盘快捷键全集（空格/Enter、1–4、`u` 撤销、`e` 编辑、`s` 暂停、`b` 埋藏） | 与 `DESIGN.md` §8.2 一致 | **已核实（脚本绑定）**：`TestAccessibilityReviewKeyboardShortcuts` 通过。**真实按键手感待 nite 手动执行。** |
| R9 | 用屏幕阅读器走一遍卡片 | 正反面、按钮、计数被正确朗读 | **待 nite 手动执行**（需读屏软件，本清单无法断言）。 |

---

## 3. 卡组页（`/decks`）

| # | 步骤 | 预期 | 实际观察 |
|---|---|---|---|
| D1 | Tab 走过「新建卡组」表单：名称、描述、预设下拉、创建按钮 | 每个字段有可见标签；下拉可用方向键选择；Tab 顺序合理 | **已核实（标签关联）**：三个字段均用**包裹式 `<label>`**（`internal/web/views/decks.templ` 的名称/描述/预设块），`TestAccessibilityFormControlsHaveLabels` 对 `/decks` 审计 5 个控件，0 个未标注。**Tab 手感与下拉键盘操作待 nite 手动执行。** |
| D2 | 卡组列表的表格 | 表头有可读列名；每行的卡组名/导出链接可聚焦且有可见文本 | **已核实**：`<thead>` 有 4 个 `<th>`（名称/卡片数/到期数/操作）；行内链接文本来自数据（`row.Name`、`data.ExportLabel`）。**可见焦点手感待 nite 手动执行。** |
| D3 | 焦点环 | 表单控件与链接获得焦点时可见 | **已核实（样式来源）**：同 R7 的全局 `:focus-visible`。**实际可见度待 nite 手动执行。** |

---

## 4. 设置页（`/settings`）

| # | 步骤 | 预期 | 实际观察 |
|---|---|---|---|
| S1 | Tab 走过「资料」表单：显示名、界面语言、时区、复习日切点 | 每个字段都有 `for`/`id` 配对的 `<label>` | **已核实**：`internal/web/views/settings.templ` 中 `settings-display-name` / `settings-locale` / `settings-timezone` / `settings-cutoff` 均为 `<label for=...>` + 对应 `id`；`TestAccessibilityFormControlsHaveLabels` 对 `/settings` 审计 9 个控件，0 个未标注。 |
| S2 | Tab 走过「改密码」表单：当前密码、新密码 | 两个密码输入框各有标签，且有 `autocomplete` 提示 | **已核实**：`settings-old-password`（`autocomplete="current-password"`）、`settings-new-password`（`autocomplete="new-password"`）均带 `for`/`id` 标签。**密码管理器/键盘填充体验待 nite 手动执行。** |
| S3 | 时区输入框（带 `datalist` 建议） | 键盘可选择建议项 | **待 nite 手动执行**（`datalist` 的键盘行为因浏览器而异）。 |
| S4 | 提交失败（如非法时区）后的提示 | 错误提示被朗读，焦点回到出错的字段 | **已核实（提示存在）**：错误块 `role="alert"`（`settings.templ`）；**焦点管理与读屏朗读待 nite 手动执行**。 |
| S5 | 焦点环 | 表单控件获得焦点时可见 | **已核实（样式来源）**：同 R7。**实际可见度待 nite 手动执行。** |

---

## 5. 附带修复：卡片列表页（`/decks/:id/notes`，编辑流程入口）

| # | 步骤 | 预期 | 实际观察 |
|---|---|---|---|
| E1 | 批量操作表单里的「标签」输入框 | 有可访问名称 | **已核实**：加了 `aria-label`（取自 `data.BulkTagPlaceholder`）；`TestAccessibilityFormControlsHaveLabels` 对 `/decks/:id/notes` 审计 10 个控件，0 个未标注。 |
| E2 | 表格里每行的选择复选框 | 有可访问名称（读屏能区分是哪张卡） | **已核实**：每行复选框带 `aria-label={ row.Front }`。**读屏实际读音待 nite 手动执行。** |

---

## 6. 本轮已核实的证据（命令输出）

以下命令均在 worktree `/home/nite/dev/engram-wt/m8-a11y` 内、于 **2026-10-02** 执行。

**无障碍断言测试（M8-5 的代码侧验收）**：

```
$ go test ./internal/web/ -run 'TestAccessibility|TestUnlabelled' -count=1 -v
=== RUN   TestUnlabelledControlsDetectsBareInput
    a11y_test.go:116: audit self-check: bare input flagged; wrapped/for=/aria-label/hidden controls pass
--- PASS: TestUnlabelledControlsDetectsBareInput (0.00s)
=== RUN   TestAccessibilityFormControlsHaveLabels
    a11y_test.go:146: review-basic: audited 13 form control(s); no unlabelled visible control found
    a11y_test.go:146: review-graded: audited 14 form control(s); no unlabelled visible control found
    a11y_test.go:146: decks: audited 5 form control(s); no unlabelled visible control found
    a11y_test.go:146: settings: audited 9 form control(s); no unlabelled visible control found
    a11y_test.go:146: notes-list: audited 10 form control(s); no unlabelled visible control found
    a11y_test.go:163: review-graded: graded answer input carries id="review-answer-input" and a matching <label for=...>
--- PASS: TestAccessibilityFormControlsHaveLabels (0.37s)
=== RUN   TestAccessibilityRatingButtonsHaveReadableNames
    a11y_test.go:192: review: 4 rating button(s) with readable labels: map[困难:true 简单:true 良好:true 重来:true]
--- PASS: TestAccessibilityRatingButtonsHaveReadableNames (0.84s)
=== RUN   TestAccessibilityFocusVisibleStyle
    a11y_test.go:209: input.css: :focus-visible rule with outline present (keyboard focus is visible)
--- PASS: TestAccessibilityFocusVisibleStyle (0.00s)
=== RUN   TestAccessibilityReviewKeyboardShortcuts
    a11y_test.go:235: review.js: keydown handler covers space/Enter, 1–4, u/e/s/b
--- PASS: TestAccessibilityReviewKeyboardShortcuts (0.00s)
PASS
ok  	example.com/engram/internal/web	1.273s
```

**脱敏与模板文案检查（提交前门槛，`git add` 之后运行）**：

```
$ bash scripts/checks/no-template-literals.sh
[template-literals] OK: no hardcoded user-facing text in 21 template(s).
$ bash scripts/checks/no-private-data.sh
[sanitize] OK: no real domains, host names, private addresses, emails or credentials found.
```

> 说明：上述证据证明的是**代码 / 渲染 HTML / 静态资源**层的结论。人工项（R1、R2/R3/R8 的手感、R7/R9 的可见度与读屏、D1/D2、S1/S3/S4、E2 的读屏）**不在**证据覆盖范围内，必须由 nite 手动执行后填写。

---

## 7. 已修复的代码点（本轮改动）

| 文件 | 改动 |
|---|---|
| `internal/web/views/review.templ` | 作答类答案输入框加 `id="review-answer-input"` 与关联 `<label for=...>`。 |
| `internal/web/views/review_data.go` | `ReviewGradedView` 加 `AnswerLabel` 字段。 |
| `internal/web/review.go` | `buildGradedView` 从语言包 `a11y.review.answer_input` 取该标签。 |
| `internal/web/views/notes.templ` | 批量标签输入框加 `aria-label`；每行选择复选框加 `aria-label={ row.Front }`。 |
| `internal/web/static/css/input.css` | 加全局 `:focus-visible` 焦点环规则。 |
| `internal/i18n/locales/{zh-CN,en}.yaml` | 末尾追加 `a11y.` 区块（2 个键，中英各一份）。 |
| `internal/web/a11y_test.go` | 新增：控件标签审计 + 负例自检、评分按钮可读文本、焦点样式、键盘快捷键绑定。 |

---

## 8. 待 nite 手动执行的汇总

- 复习页：R1（Tab 顺序手感）、R2/R3/R8（键盘评分与快捷键真实手感）、R7（焦点环对比度）、R9（读屏）。
- 卡组页：D1（表单 Tab 手感、下拉键盘操作）、D2（列表焦点手感）、D3（焦点环可见度）。
- 设置页：S3（`datalist` 键盘）、S4（错误提示读屏与焦点回位）、S5（焦点环可见度）。
- 附带：E2（每行复选框的读屏读音）。

---

## 9. 手动执行记录（由 nite 填写）

| 日期 | 设备 / 浏览器 | 读屏 | 执行的项 | 观察结论 / 异常 |
|---|---|---|---|---|
| 待填 | 待填 | 待填 | 待填 | 待填 |

执行完请把本节每一行的「观察结论」写成可直接引用的句子（含截图路径更佳），并保持本文档顶部日期为「编写与代码侧核实日期」，另在此表记录手动执行日期。