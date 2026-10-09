# 代码审查问题记录

审查日期：2026 年 10 月 9 日。审查基线：`3e296387f334c3b07d1262093820d1165d98b000`。

初次检查覆盖后端业务、前端入口和已有测试，发现笔记编辑、评分撤销、克隆校验及资源限制问题。原问题描述和初次证据保留在复查记录之后；当前处理状态以最近一轮复查结论为准。修复建议不是已经确定的产品要求；涉及持久化模型或功能范围的选择，由维护者决定。

“已复现”表示补充测试已经触发问题。“静态发现”表示依据代码确认实现或推导风险，尚未完成对应的运行时复现。“待设计决定”表示仓库规则与实现冲突，或底层能力尚未形成用户功能。

## 第二轮修复复查

复查日期：2026 年 10 月 9 日。复查基线：`41a3906`。检查范围为 `54c29c7..41a3906` 的修复提交、上轮四个问题的原复现场景及相关回归测试。四个问题均通过本轮验证；本轮修改未发现新的可确认缺陷。这个结论不代表重新审查了项目的全部代码。

| 编号 | 本轮验证结果 |
| --- | --- |
| RECHECK-01 | 通过。查询回调在读取卡组后写入预设 2；信息修改完成后预设仍为 2。原可控交错测试不再触发覆盖。 |
| RECHECK-02 | 通过。真实 REST PATCH 仅提供名称后，原描述仍为 `keep me`。已有测试同时覆盖仅改描述、显式清空描述和非法名称。 |
| RECHECK-03 | 通过。Chromium 加载本轮生产构建；单卡组切换、多卡组范围变化、后退和前进均重新请求对应范围的队列，并显示对应卡片。 |
| RECHECK-04 | 通过。埋藏、评分、导出含进度及日志的包、导入为新卡组，再撤销；恢复后的到期时间与导出前埋藏时间一致。已有包往返测试另行确认学习步骤快照为 1 时仍被保留。 |

原复现场景的本轮输出：

```text
concurrent preset change=2; after name update=2
omitted description: persisted="keep me"
imported due_before=2026-10-03 04:00:00 +0000 UTC
before export due=2026-10-03 04:00:00 +0000 UTC
after imported undo due=2026-10-03 04:00:00 +0000 UTC
BEFORE       url=/review?deck=A        front=Question card-A
SWITCH       url=/review?deck=B        front=Question card-B
BACK         url=/review?deck=A        front=Question card-A
FORWARD      url=/review?deck=B        front=Question card-B
MULTI        url=/review?deck=A&deck=B front=Question card-A
MULTI_CHANGE url=/review?deck=C&deck=B front=Question card-C
```

浏览器测试使用本地 HTTP 测试响应；路由、组件挂载和队列请求来自真实前端生产构建。上述每次范围切换均捕获到对应的 `/api/v1/review/due` 请求。补充 Go 测试通过临时 overlay 运行，没有写入业务源码。

本轮检查输出：

- `go test ./internal/api ./internal/store ./internal/schedule ./internal/mcp` 通过；这些包的 `go vet` 通过。
- 通过临时 Go overlay 重跑三个原复现测试：全部通过。测试名称分别为 `TestRecheckDeckInfoDoesNotOverwritePreset`、`TestRecheckDeckPatchOmittedDescription`、`TestRecheckUndoSnapshotSurvivesPackageRoundTrip`。这些补充测试没有保存进仓库。
- `npm --prefix frontend run check`：0 errors、0 warnings。
- `npm --prefix frontend test -- --run`：44 个测试文件、370 个测试通过。
- `npm --prefix frontend run build`：通过；仍输出 JavaScript chunk 超过 500 kB 的构建提示。
- 前端构建完成后，`go build ./... && go vet ./... && gofmt -l . && go test ./...` 通过；格式检查没有输出。
- `go test ./internal/schedule -run TestUndoConcurrentSameVersionIsAtomic -v`：因未设置 `TEST_PG_DSN` 跳过。AUDIT-03 的真实 PostgreSQL 并发验收仍未完成。

归档、回收站和自定义判分映射仍为维护者已说明的未实现范围。它们不计为本轮修复遗漏。本轮只更新审查记录，没有修改业务行为。

## 修复后的复查

复查日期：2026 年 10 月 9 日。复查基线：`7fe61bc`。复查范围为初次审查后新增的修复提交。维护者说明归档、回收站、自定义判分映射三个功能尚未实现，本次不把这三个功能计为修复遗漏。

| 原编号 | 复查结论 |
| --- | --- |
| AUDIT-01 | 已修复：新增单条读取接口，编辑页直接读取笔记并检查所属卡组 |
| AUDIT-02 | 已修复：撤销携带预期版本，重放和旧版本测试通过 |
| AUDIT-03 | 已调整为先锁状态并校验版本，再读日志；真实 PostgreSQL 验收仍未执行 |
| AUDIT-04 | 当前库内的埋藏到期时间恢复测试通过；包迁移遗漏见 RECHECK-04 |
| AUDIT-05 | 已修复：克隆执行统一校验，网页副本名按字符截断源名称 |
| AUDIT-06 | 已修复：单页上限为 200，页码上限为 1000000 |
| AUDIT-07 | 路径参数变化时重建组件；复习范围查询参数变化已修复，见 RECHECK-03 |
| AUDIT-08 | 已按返回的 reader 角色隐藏内容修改入口；个人暂停和学习设置入口保留 |
| AUDIT-09 | REST、MCP 和网页入口已接通；RECHECK-01 和 RECHECK-02 已修复 |
| AUDIT-10 | 归档入口仍未实现，维护者已说明该范围 |
| AUDIT-11 | 自定义映射入口仍未实现，维护者已说明该范围 |
| AUDIT-12 | 回收站入口仍未实现，维护者已说明该范围 |
| AUDIT-13 | 已解决规则冲突：AGENTS.md 明确允许撤销删除目标评分，并要求审计记录 |

### RECHECK-01 修改卡组信息会覆盖期间更新的调度预设

优先级：高。证据：真实 SQLite 数据库上的可控交错复现。

**已修复**（`92c4a21`）：`DeckStore.Update` 只更新 `name` 与 `description`，不再写 `preset_id`；调度预设只经 `SetPreset` / `SetStudySettings` 变更。验收：`internal/api` 的 `TestUpdateDeckKeepsPreset` 与 `internal/store` 的 `TestDeckStoreCRUD`（改名后预设保持原值）。

位置：[service.go](internal/api/service.go)，`API.UpdateDeck`；[deck.go](internal/store/deck.go)，`DeckStore.Update`。

信息修改接口先读取卡组的 `preset_id`，把这个旧值放入待更新模型，再调用同时写入名称、描述和 `preset_id` 的存储方法。若学习设置在两次操作之间改成另一预设，信息修改仍会把旧预设写回。名称和描述的修改不应写入调度预设列。

补充测试在服务首次读取卡组的查询回调中写入另一预设，用来确定性模拟读取后发生的设置更新。新预设 ID 为 2；调用信息修改后，持久化预设回退为 1。测试没有依赖线程调度概率。

建议为信息修改提供只更新 `name` 和 `description` 的存储方法。验收应控制交错顺序，确认信息保存不会覆盖期间更新的预设或每日上限。

### RECHECK-02 PATCH 省略描述会清空原描述

优先级：中。证据：真实 SQLite 数据库和 REST handler 复现。

**已修复**（`94a0ff1`）：REST 请求体、共享业务入参与 MCP 入参改用指针区分「未提供」与「显式空串」；存储层按 PATCH 语义只写给出的字段，校验针对合并后的最终文本。验收：`TestUpdateDeckPartialUpdateKeepsOmittedFields`、`TestUpdateDeckHTTPPartialBodyKeepsDescription`、`TestUpdateDeckRejectsInvalidText`。

位置：[decks.go](internal/api/decks.go)，`updateDeckRequest`；[service.go](internal/api/service.go)，`UpdateDeckInput`；[tools.go](internal/mcp/tools.go)，`updateDeckIn`。

REST 请求和 MCP 输入用普通字符串承载名称、描述，无法区分字段缺失与显式空字符串。请求注释允许两个字段缺省，但实现将缺失描述当成空描述写入，并将缺失名称当成非法空名称拒绝。

补充测试先保存描述 `keep me`，再发送 `PATCH /api/v1/decks/:id`，请求体仅为 `{"name":"renamed"}`。请求返回 200，原描述被清空为 `""`。

建议在 REST、MCP 和共享业务输入中区分字段是否提供：缺失保持原值，显式空描述用于清空，显式空名称被拒绝。验收应分别覆盖仅修改名称、仅修改描述、清空描述和非法名称。

### RECHECK-03 复习范围查询参数变化不会重载队列

优先级：中。证据：真实 Chromium 加载生产构建后的交互复现；HTTP 响应使用本地测试数据。

**已修复**（`17cb995`）：`viewKey` 纳入原始查询串（按段排序，重复键保留），复习范围变化即触发组件重建并重新取队列；查询串各段顺序变化不重建。本轮浏览器验证通过，见第二轮修复复查。

位置：[router/index.ts](frontend/src/lib/router/index.ts)，`viewKey`；[ReviewView.svelte](frontend/src/lib/views/ReviewView.svelte)，`selectedDecks` 和 `onMount`；[App.svelte](frontend/src/App.svelte)，组件重建边界。

`viewKey` 排除了全部查询参数。复习页的卡组范围位于 `deck` 查询参数中，队列只在挂载时读取，而评分、埋藏等请求每次从当前地址读取范围。切换范围后，旧卡片与新范围会同时进入页面的后续操作。

浏览器先打开 `/review?deck=A`，再通过站内链接切换到 `/review?deck=B`。捕获结果如下：

```text
BEFORE url=/review?deck=A front=Question card-A
AFTER  url=/review?deck=B front=Question card-A
DUE_REQUESTS /api/v1/review/due?deck=A&limit=500
```

地址变为 B 后仍显示 A 的卡片，没有发出 B 的队列请求。此时评分请求会携带 A 的卡片和 B 的范围，后端范围校验会拒绝。

建议让复习范围变化触发组件重建或显式重载队列。验收应覆盖单卡组切换、多卡组范围变化，以及前进和后退；不需要因为无关查询参数变化而清空复习过程。

### RECHECK-04 卡组包迁移丢失撤销快照

优先级：中。证据：真实 SQLite 数据库上的导出、导入和撤销复现。

**已修复**（`d4244f2`）：`PackageReview` 增加 `step_index_before` 与 `due_before` 两个可空字段，导出写入、导入写回，`schema/deck-package.schema.json` 同步补记；旧包缺字段时仍按「回退到上一条日志推算」处理。验收：`TestPackageRoundTripKeepsReviewUndoSnapshots`。

位置：[package.go](internal/store/package.go)，`PackageReview` 和 `exportProgress`；[package_import.go](internal/store/package_import.go)，`applyProgress`；[models.go](internal/store/models.go)，`Review.DueBefore`；[actions.go](internal/schedule/actions.go)，`Rollback`。

新增的 `due_before` 快照没有加入卡组包的评分记录结构、导出映射和导入映射。已有的 `step_index_before` 也未经过该包传输。数据库内撤销恢复测试通过，不能证明包含进度及日志的备份恢复后仍能还原评分前状态。

补充测试先埋藏新卡，再评分，随后导出包含进度和评分日志的卡组包，导入为新卡组，然后撤销导入卡片的最后一次评分。捕获结果如下：

```text
imported due_before=<nil> step_index_before=<nil>
before export due=2026-10-03 04:00:00 +0000 UTC
after imported undo due=<nil>
```

建议同步包的数据结构、导出、导入及相应 schema，使新包保留撤销所需的快照。旧包缺字段时继续采用明确的兼容行为。验收应覆盖埋藏、学习步骤和普通复习的进度日志往返。

### 复查验证结果

`go build ./... && go vet ./... && gofmt -l . && go test ./...` 通过，格式检查没有输出。`npm --prefix frontend run check` 返回 0 errors、0 warnings；前端 44 个测试文件、369 个测试通过；生产构建通过。

已有的撤销重放、旧版本拒绝和埋藏到期时间恢复测试通过。`TestUndoConcurrentSameVersionIsAtomic` 因未设置 `TEST_PG_DSN` 而跳过，因此 AUDIT-03 的结论仍限于代码执行顺序检查，不能记为已通过 PostgreSQL 验收。

三个补充 Go overlay 测试失败，分别证明 RECHECK-01、RECHECK-02 和 RECHECK-04。RECHECK-03 已在 Chromium 中复现。补充测试和浏览器测试使用临时文件，没有修改业务源码。本次复查只更新问题记录。

## 初次审查问题清单

| 编号 | 优先级 | 问题 | 证据状态 |
| --- | --- | --- | --- |
| AUDIT-01 | 高 | 编辑页无法加载第一页之外的笔记 | 静态发现 |
| AUDIT-02 | 高 | 重复撤销请求会额外删除历史评分 | 已复现 |
| AUDIT-03 | 高 | PostgreSQL 撤销可能使用过时日志恢复新状态 | 静态发现，待 PostgreSQL 复现 |
| AUDIT-04 | 中 | 撤销无法完整恢复评分前的埋藏到期时间 | 已复现 |
| AUDIT-05 | 中 | 克隆绕过卡组名统一校验 | 已复现 |
| AUDIT-06 | 中 | 笔记分页没有单页数量上限 | 静态发现 |
| AUDIT-07 | 中 | 同类页面切换路由参数时可能保留旧内容 | 静态发现，待浏览器复现 |
| AUDIT-08 | 中 | 只读成员看到无法执行的内容修改入口 | 静态发现 |
| AUDIT-09 | 待定 | 卡组名称和描述缺少修改入口 | 待设计决定 |
| AUDIT-10 | 待定 | 卡组归档和恢复缺少用户入口 | 待设计决定 |
| AUDIT-11 | 待定 | 自动判分映射缺少配置入口 | 待设计决定 |
| AUDIT-12 | 待定 | 已删除笔记缺少浏览和恢复入口 | 待设计决定 |
| AUDIT-13 | 高 | 删除评分日志与 append-only 规则冲突 | 待设计决定 |

## 业务和前端问题

### AUDIT-01 编辑页只搜索前 100 条笔记

位置：[NoteEditView.svelte](frontend/src/lib/views/NoteEditView.svelte)，`load`；[note.go](internal/store/note.go)，`NoteStore.List`。

编辑页固定请求 `page: 1, per_page: 100`，再从结果中查找目标笔记 ID。后端按 `created_at DESC, id DESC` 返回笔记，因此列表位置超过 100 的笔记不会出现在编辑页的查找范围内。页面把查找失败显示为笔记不存在。

触发步骤：在同一卡组创建至少 101 条笔记；打开卡组列表第三页；点击较早笔记的编辑链接。复习页的编辑链接也会受到同一限制。

建议提供按对外 ID 读取单条笔记的接口，编辑页直接读取目标。验收应覆盖第一页之外的笔记、未知 ID、无读取权限，以及笔记与路径卡组不匹配的情况。

### AUDIT-02 重复撤销会删除另一条评分

位置：[review_undo.go](internal/api/review_undo.go)，`UndoReviewInput`；[review_api.go](internal/web/review_api.go)，`reviewUndo`；[actions.go](internal/schedule/actions.go)，`Rollback`。

撤销请求只指定卡片，没有指定目标评分 ID 或预期状态版本。每次请求都会重新选择当前最后一条评分。因此，同一请求被重发时，第二次操作会删除前一条历史评分。界面防止连续点击不能保护网络重试或多个窗口发出的请求。

补充测试先写入两条评分，再执行两次相同的 `UndoInput`。两次调用都成功，剩余评分数为 0；如果第二次请求被识别为重放，剩余评分数应为 1。

建议撤销绑定目标评分记录，并校验状态版本。重复请求的响应方式需要确定，但不得因此删除另一条评分。验收应覆盖重复请求、旧版本请求、两个窗口操作，以及另一条评分已经提交的情况。

### AUDIT-03 PostgreSQL 撤销的日志读取和状态锁定存在竞态

位置：[actions.go](internal/schedule/actions.go)，`Rollback`；[submit.go](internal/schedule/submit.go)，`loadStateForUpdate`。

`Rollback` 先读取最近两条评分，再读取并锁定状态行。两步之间，另一事务可以提交新评分。撤销随后读到新状态，却仍持有旧评分日志。写入守卫使用随后读取到的状态版本，因此不能检测日志与状态的错配。

需在 PostgreSQL 上验证以下事务交错：撤销读取日志 A；另一事务提交评分 B；撤销锁定 B 对应的状态；撤销使用 A 的日志恢复状态并删除 A。SQLite 部署采用单连接，现有 SQLite 测试不能证明 PostgreSQL 下不存在该问题。

建议在锁定状态后读取目标日志，并确认日志对应此次撤销的版本或评分 ID。验收应使用可控制执行顺序的 PostgreSQL 并发测试，检查状态、评分记录和审计记录的一致性。

### AUDIT-04 撤销缺少完整的评分前快照

位置：[actions.go](internal/schedule/actions.go)，`rebuildReviewLog`；[submit.go](internal/schedule/submit.go)，`reviewFromOutcome`。

撤销主要依靠上一条评分推算旧到期时间。首次评分没有上一条日志时，到期时间被还原为 `NULL`。评分前发生的埋藏会修改到期时间，但该值没有保存在评分前快照中。

补充测试先埋藏新卡，再在下一天评分，然后撤销。评分前到期时间为 `2026-10-03 04:00:00 UTC`，撤销后为 `NULL`。该结果不符合代码注释承诺的精确恢复。

建议保存足以还原评分前状态的快照。快照字段和历史日志的回退行为需要明确决定。验收应覆盖首次评分前曾被埋藏、两次评分之间曾被埋藏，以及普通评分的撤销。新增快照不得混入其他用户的状态。

### AUDIT-05 克隆绕过统一名称校验

位置：[clone.go](internal/store/clone.go)，`DeckStore.Clone`；[clone.go](internal/web/clone.go)，`deckClone`；[deck.go](internal/store/deck.go)，`validateDeckName`。

克隆仅检查名称非空，然后直接调用 `tx.Create`，没有执行卡组创建入口使用的统一名称校验。补充测试成功克隆出名称长 201 字符的卡组，而统一校验上限为 200 字符。

网页克隆会给源名称追加中文或英文副本后缀。长度接近上限的合法源名称会因此生成超长副本名称。该副本后续经过名称校验的写入或包导入入口时，会被拒绝。

建议克隆复用统一校验。如何处理副本后缀导致的超长名称，需要决定明确策略。验收应覆盖接近上限的名称、中文字符、重复克隆，以及克隆结果的导出再导入。

### AUDIT-06 笔记分页缺少最大条数限制

位置：[note.go](internal/store/note.go)，`NormalizeNoteListOptions` 和 `NoteStore.List`；[notes.go](internal/api/notes.go)，`listNotes`。

分页只修正非正 `per_page`，没有最大值。客户端可以提交极大的正数，使单次请求加载大量笔记字段并构造完整 JSON 响应。限制请求频率不能限制一次请求的内存占用。

建议确定单页数量上限，并在共享参数归一化函数内执行限制，使 REST、MCP 和存储调用保持一致。验收应覆盖极大分页数、正常分页，以及极大页码产生的偏移量计算。

### AUDIT-07 路由参数变化可能保留旧页面数据

位置：[App.svelte](frontend/src/App.svelte)，`ActiveComponent`；[DeckDetailView.svelte](frontend/src/lib/views/DeckDetailView.svelte)，`onMount` 和 `loadData`；[NoteEditView.svelte](frontend/src/lib/views/NoteEditView.svelte)，`onMount(load)`；[DeckSettingsView.svelte](frontend/src/lib/views/DeckSettingsView.svelte)。

应用根据页面组件类型渲染视图，没有按完整路由或实体 ID 建立组件重建边界。上述视图主要在挂载时读取数据。站内路由直接切换到同一组件的另一组参数时，实体 ID 会变化，但数据加载过程未必重新执行。卡组详情还只在 `deck` 为空时读取卡组元数据。

待浏览器验证：同一组件从卡组 A 切到 B、从笔记 A 切到 B、同类页面之间前进和后退。检查页面标题、表单内容及写请求目标是否一致。

建议监听实体 ID 的变化并重置视图状态，或按路由建立组件重建边界。需要同时防止旧请求晚于新请求完成时覆盖新页面数据。

### AUDIT-08 只读成员看到内容修改按钮

位置：[DeckDetailView.svelte](frontend/src/lib/views/DeckDetailView.svelte)，新建入口、笔记编辑和删除入口、批量操作工具栏。

详情页没有根据卡组角色隐藏新建、编辑、删除和批量修改标签的入口。reader 可以看到并点击这些按钮，但服务端会拒绝内容写入。这是界面权限表达问题；本次未发现这些入口绕过后端权限校验。

建议按服务端返回的角色显示内容修改入口。reader 应继续保留个人暂停、取消暂停和学习设置入口，这些操作只修改本人进度或设置。

## 已有底层能力与用户入口缺口

### AUDIT-09 修改卡组名称和描述

[DeckStore.Update](internal/store/deck.go) 已有属主校验、文本校验及写入实现。当前 REST 路由没有对应的修改名称和描述接口；前端可以在创建时输入名称和描述，但没有创建后的编辑入口。

需要决定是否把该底层能力作为用户功能接通。若接通，应同时补接口和表单，并验证非属主拒绝、文本上限、空描述和失败后不写入。

### AUDIT-10 卡组归档和恢复

[DeckStore.Archive 和 DeckStore.Restore](internal/store/deck.go) 已实现归档字段写入，但没有对应的用户接口和页面入口。

需要先决定归档对列表、队列、共享成员和统计的具体影响。已有字段和存储方法不足以确定这些产品语义。

### AUDIT-11 自动判分映射配置

[Preset.SetGradeMapping](internal/store/preset.go) 已实现映射校验和 JSON 写入；评分业务会读取预设映射。当前预设请求体及前端表单没有暴露该字段，用户无法通过页面配置映射。

需要决定是否接通自定义映射功能。若接通，应补读取响应、写入校验、表单和恢复默认映射入口，并验证评分业务使用保存后的值。

### AUDIT-12 已删除笔记的浏览和恢复

[NoteStore.List 和 NoteStore.Restore](internal/store/note.go) 支持查询软删除笔记及恢复记录。前端详情页没有已删除筛选或回收站，恢复主要通过批量导入命中已删除的 `external_ref` 完成。

需要决定是否提供显式回收站。若提供，应明确可恢复角色、恢复后的卡片和个人进度行为，并接通恢复接口。不能把存在底层恢复方法等同于已有完整的恢复功能。

## 规则与实现冲突

### AUDIT-13 撤销硬删除评分日志

[AGENTS.md](AGENTS.md) 第 2.3 节要求 `reviews` 为 append-only。[actions.go](internal/schedule/actions.go) 的 `Rollback` 却硬删除被撤销的评分，且注释将撤销描述为例外。现有测试也要求删除被撤销记录。

需要由维护者决定：保留不可变评分记录并增加撤销表示，或正式修改仓库规则以允许明确的例外。若保留日志，需要同步定义优化器、统计和导出如何排除已撤销评分。本报告不选择其中任何方案。

## 验证结果与限制

基线验证结果如下：

| 命令 | 结果 |
| --- | --- |
| `go test ./...` | 除三个受沙箱端口限制的包之外，其余包通过 |
| `go test ./internal/mail ./internal/mcp ./internal/web` | 解除沙箱端口限制后，三个包全部通过 |
| `npm --prefix frontend run check` | 0 errors，0 warnings |
| `npm --prefix frontend test -- --run` | 44 个测试文件通过，359 个测试通过 |
| `go vet ./...` | 通过 |
| `npm --prefix frontend run build` | 通过；主 JavaScript 包约 774.55 kB，gzip 后约 209.37 kB，构建器提示可拆包 |
| `go build ./...` | 通过 |
| `gofmt -l .` | 无输出 |

三个补充 SQLite 测试使用 Go overlay 加载测试源码，没有写入仓库的业务文件。失败输出中的关键事实为：

```text
TestAuditUndoAfterBury
before review: due=2026-10-03 04:00:00 +0000 UTC; after undo: due=<nil>
undo did not restore pre-review due_at

TestAuditCloneNameValidation
clone accepted 201-character name, new deck id=2

TestAuditReplayUndo
identical undo request twice: remaining reviews=0
replayed undo removed an additional review
```

复现过程分别是 AUDIT-04 的“埋藏新卡、评分、撤销”、AUDIT-05 的“传入 201 字符名称进行克隆”、AUDIT-02 的“两条评分后两次执行相同撤销输入”。这些测试证明当前行为与对应的保护性断言不一致。

本次没有设置 `TEST_PG_DSN`，PostgreSQL 门控测试未执行。AUDIT-03 仍需实际 PostgreSQL 并发复现。前端检查包含类型检查、现有组件测试和生产构建，不包含真实浏览器交互；AUDIT-07 仍需浏览器复现。

现有前端测试包含 API mock、源码检查和服务端渲染断言。这些测试不能代替分页数据规模、浏览器生命周期和真实请求重放的验证。已有测试全部通过，不能否定本报告列出的遗漏。

## 处理顺序

建议先处理 AUDIT-01 的单条笔记读取，以及 AUDIT-02、AUDIT-03、AUDIT-04 的撤销目标、事务顺序和评分前快照。AUDIT-13 的日志保存决定会影响撤销实现，应与撤销修复一起确定。

随后处理克隆校验、分页上限、路由生命周期和角色对应的界面入口。AUDIT-09 至 AUDIT-12 应在维护者确定功能范围和语义后再实施。本报告不修改 ROADMAP.md。
