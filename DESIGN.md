# Flashcard 服务设计文档

> 状态：设计稿 v2（2026-10-01 依据评审意见修订）。
> **项目名待定**，全文用 `flashcard` / `<module-path>` 作占位。
> 本文件随代码开源，因此**不含任何个人或私有基础设施信息**（不写具体域名、主机名、内网地址、端口分配、个人使用场景）；示例一律用占位符。仓库内所有文件（配置、脚本、测试数据、文档）都必须遵守同一条脱敏要求。
> 本文档描述**完整最终态**（含尚不实现的未来规划，见 §14），实现按 §12 的里程碑分阶段推进；设计上优先为将来留口子，避免推倒重来。

---

## 1. 定位与范围

**一句话**：网页优先、多用户、自托管的间隔重复（SRS）记忆服务。卡片可在网页上手工创建，也可由外部系统/Agent 通过本服务的 **用户级 API + Key** 或**内置 MCP server** 写入；复习排程基于 FSRS v6。服务本身不解析任何外部笔记系统。

**目标用户**

- 备考/学习者个人，以及与他共享卡组的少量同学、朋友（多为非技术用户，以手机浏览器为主，各自独立进度）
- 需要把"资料 → 卡片"自动化的技术用户（用 API/MCP 对接自己的笔记系统或 Agent）

**核心需求（硬约束）**

1. **网页优先**：所有功能（含注册、管理、系统设置）都在浏览器里完成，不要求使用 CLI；不做桌面客户端、不做原生 App。
2. **多用户**：每人独立账号与复习进度；卡组可共享（只读 / 读写，可随时撤销）。
3. **自托管**：数据 100% 在自己的机器上，不依赖任何外部 SaaS。
4. **对外集成面**：用户级 API + Key，以及**内置 MCP server**，供外部系统/Agent 建卡、改卡、取到期、提交复习。服务自身不做导入器、不认识任何具体笔记系统（出卡逻辑全部在外部）。
5. **排程用 FSRS**，不自己写记忆算法。
6. **多语言界面**：首发中文 + 英文（i18n 架构就位，新增语言只加语言包）。
7. **管理面板**：用户管理、注册策略、系统设置（上传限制、OIDC 开关等）全部 web 化。
8. **题型可扩展**：内置题型尽量丰富，且新增题型不改核心代码（注册表模式）。

**明确的非目标（决定不做的，写下来避免漂移）**

| 非目标 | 原因 |
|---|---|
| 兼容 Anki `.apkg` / `.colpkg` 格式 | 已核实其复杂度：zip 内含 `collection.anki2`/`collection.anki21`/`collection.anki21b` 三代文件名，最新版为 zstd 压缩 + schema V18 + protobuf `meta` 文件；复刻成本远超收益（来源：`ankitects/anki` → `rslib/src/import_export/package/meta.rs`） |
| 桌面/移动原生客户端 | 需求即是"网页优先、别人不用装东西" |
| 离线优先 / 离线答题 | 网页优先意味着必须联网；只做 PWA 外壳缓存（§8.5），答题数据不落本地，避免同步冲突 |
| 多人同时编辑同一卡组内容（协作编辑） | 需要引入冲突解决、编辑历史、权限细分；需求只要求"能共享"（§5） |
| 外部系统解析器（某笔记系统 A/B/C…） | 服务不认识任何外部笔记系统，也不内置导入器；集成由外部经 API/MCP 完成 |
| 当前范围内做 LLM 调用 | 内置 LLM 接入与主观题 LLM 评分列入**未来规划**（§14），本期只留设计口子 |
| 公开题库市场 / 社交功能 | 自用 + 小圈子，不需要发现、排行、关注 |
| 多租户 / 组织层级 | 单实例单组织，不做 tenant 隔离 |

---

## 2. 领域模型

### 2.1 概念

| 概念 | 说明 |
|---|---|
| **User** | 本地账号（用户名/邮箱 + 密码哈希），可另绑定零到多个外部身份（OIDC）。 |
| **Identity** | 外部身份（provider + subject），绑定到某个内部 User；一个用户可有多个。 |
| **Deck（卡组）** | 卡片集合，也是调度配置（preset）与共享（授权）的作用域。 |
| **Note（笔记/卡片内容）** | "一个事实"。含题型与字段，**只描述内容，不含任何用户进度**。 |
| **Card（呈现形式）** | 一张 note 在某种题型下的呈现（正向、反向、挖空项…）。调度作用于 card。 |
| **CardState（用户进度）** | 某个用户对某张 card 的 FSRS 状态。**主键 (card_id, user_id)**——多用户共享卡组的基石。 |
| **Review（复习日志）** | 不可变 append-only 记录，每次评分写一行；统计与参数优化的唯一数据源。 |
| **Preset（调度预设）** | 一组调度参数（目标保留率、学习步骤、最大间隔、fuzz、FSRS 权重），挂在 deck 上。 |
| **Grant（授权）** | 用户对 deck 的角色：owner / editor（读写）/ reader（只读）。 |
| **API Key** | 用户级凭据，供外部系统/Agent 调用 REST 与 MCP。明文只显示一次，库里存哈希（§7.2）。 |
| **Job** | 后台任务（当前只有参数优化），web 触发、子进程执行、页面轮询状态（§3.5）。 |
| **Setting** | 系统级配置（注册策略、上传上限、OIDC 参数…），管理员在 web 上改，存库（§8.4）。 |

**最重要的一条设计原则：内容与进度分离。** "这张卡长什么样"是共享的，"我什么时候该复习它"是每人私有的。合成一张表，将来拆表 + 迁数据都补不回来。

### 2.2 表结构

下表是**逻辑 schema**（用 SQL 写便于阅读）；实际落库由 **GORM 模型 + AutoMigrate** 生成，同时兼容 **PostgreSQL 与 SQLite**。

```sql
-- 用户：内置账号为默认身份来源；外部身份见 identities
CREATE TABLE users (
  id            INTEGER PRIMARY KEY,
  username      TEXT NOT NULL UNIQUE,           -- 登录名
  email         TEXT NOT NULL UNIQUE,
  email_verified_at TEXT,
  password_hash TEXT,                           -- argon2id；OIDC-only 账号可为 NULL
  display_name  TEXT,
  role          TEXT NOT NULL DEFAULT 'user',   -- admin | user
  status        TEXT NOT NULL DEFAULT 'active', -- active | disabled
  locale        TEXT NOT NULL DEFAULT 'zh-CN',  -- 界面语言
  timezone      TEXT NOT NULL DEFAULT 'Asia/Shanghai',
  day_cutoff_hour INTEGER NOT NULL DEFAULT 4,   -- "新的一天"从本地时间 4:00 起算
  created_at    TEXT NOT NULL,                  -- RFC3339 UTC
  last_seen_at  TEXT
);

-- 外部身份（OIDC）：可选功能，与内部用户绑定
CREATE TABLE identities (
  id            INTEGER PRIMARY KEY,
  user_id       INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  provider      TEXT NOT NULL,                  -- issuer 标识（如 issuer host）
  subject       TEXT NOT NULL,                  -- OIDC sub
  email         TEXT,
  linked_at     TEXT NOT NULL,
  UNIQUE (provider, subject)
);

-- 邀请（注册策略为 invite 时使用；token 一次性）
CREATE TABLE invites (
  id            INTEGER PRIMARY KEY,
  token         TEXT NOT NULL UNIQUE,
  email         TEXT,                           -- 可选：限定邮箱
  role          TEXT NOT NULL DEFAULT 'user',
  created_by    INTEGER REFERENCES users(id),
  created_at    TEXT NOT NULL,
  expires_at    TEXT,
  used_at       TEXT,
  used_by       INTEGER REFERENCES users(id)
);

-- 系统设置（管理员在 web 上改；覆盖内置默认值）
CREATE TABLE settings (
  key           TEXT PRIMARY KEY,
  value         TEXT NOT NULL,                  -- JSON 编码，便于嵌套配置
  updated_by    INTEGER REFERENCES users(id),
  updated_at    TEXT NOT NULL
);

-- 卡组
CREATE TABLE decks (
  id              INTEGER PRIMARY KEY,
  owner_user_id   INTEGER NOT NULL REFERENCES users(id),
  name            TEXT NOT NULL,
  description     TEXT NOT NULL DEFAULT '',
  visibility      TEXT NOT NULL DEFAULT 'private',  -- private | unlisted | public
  preset_id       INTEGER NOT NULL REFERENCES presets(id),
  archived_at     TEXT,
  created_at      TEXT NOT NULL
);

-- 调度预设（多个 deck 可共用；preset 属创建者）
CREATE TABLE presets (
  id                  INTEGER PRIMARY KEY,
  owner_user_id       INTEGER NOT NULL REFERENCES users(id),
  name                TEXT NOT NULL,
  desired_retention   REAL NOT NULL DEFAULT 0.90,   -- 目标保留率（FSRS 最重要的旋钮）
  learning_steps      TEXT NOT NULL DEFAULT '1m,10m',
  relearning_steps    TEXT NOT NULL DEFAULT '10m',
  maximum_interval_days INTEGER NOT NULL DEFAULT 36500,
  enable_fuzz         INTEGER NOT NULL DEFAULT 1,
  weights_json        TEXT,                          -- NULL=DefaultWeights()；优化后写 21 维数组
  weights_optimized_at TEXT,
  weights_review_count INTEGER,
  created_at          TEXT NOT NULL,
  updated_at          TEXT NOT NULL
);

-- 笔记（内容，不含进度）
CREATE TABLE notes (
  id            INTEGER PRIMARY KEY,
  deck_id       INTEGER NOT NULL REFERENCES decks(id) ON DELETE CASCADE,
  kind          TEXT NOT NULL DEFAULT 'basic',   -- 题型标识，见 §6.2
  fields_json   TEXT NOT NULL,                   -- 题型决定字段结构（服务端校验）
  tags_json     TEXT NOT NULL DEFAULT '[]',
  external_ref  TEXT,                            -- 由调用方定义的幂等键（§7.3）
  source        TEXT,                            -- manual | api | import
  reference_refs TEXT,                           -- 未来：绑定的知识库片段引用（§14，先留字段不建 UI）
  created_by    INTEGER REFERENCES users(id),
  created_at    TEXT NOT NULL,
  updated_at    TEXT NOT NULL,
  deleted_at    TEXT,                            -- 软删除（保留进度，误删可恢复）
  UNIQUE (deck_id, external_ref)
);
CREATE INDEX idx_notes_deck ON notes(deck_id) WHERE deleted_at IS NULL;

-- 卡片（note 的呈现形式，调度作用于它）
CREATE TABLE cards (
  id            INTEGER PRIMARY KEY,
  note_id       INTEGER NOT NULL REFERENCES notes(id) ON DELETE CASCADE,
  template      TEXT NOT NULL,                   -- forward | reverse | cloze:<index> | ...
  ordinal       INTEGER NOT NULL DEFAULT 0,
  suspended_at  TEXT,
  created_at    TEXT NOT NULL,
  deleted_at    TEXT,
  UNIQUE (note_id, template)
);
CREATE INDEX idx_cards_note ON cards(note_id);

-- 复习状态（每用户每卡一行）
CREATE TABLE card_states (
  card_id       INTEGER NOT NULL REFERENCES cards(id) ON DELETE CASCADE,
  user_id       INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  state         TEXT NOT NULL DEFAULT 'new',     -- new | learning | review | relearning
  due_at        TEXT,                            -- UTC
  step_index    INTEGER NOT NULL DEFAULT 0,
  stability     REAL,
  difficulty    REAL,
  reps          INTEGER NOT NULL DEFAULT 0,
  lapses        INTEGER NOT NULL DEFAULT 0,
  scheduled_days INTEGER NOT NULL DEFAULT 0,
  elapsed_days  INTEGER NOT NULL DEFAULT 0,
  last_review_at TEXT,
  version       INTEGER NOT NULL DEFAULT 0,      -- 乐观锁：评分提交校验
  PRIMARY KEY (card_id, user_id)
);
CREATE INDEX idx_states_due ON card_states(user_id, due_at);

-- 复习日志（append-only，永不修改）
CREATE TABLE reviews (
  id            INTEGER PRIMARY KEY,
  card_id       INTEGER NOT NULL REFERENCES cards(id),
  user_id       INTEGER NOT NULL REFERENCES users(id),
  rating        INTEGER NOT NULL,                -- 1=Again 2=Hard 3=Good 4=Easy（对齐 FSRS 生态）
  grade_source  TEXT NOT NULL DEFAULT 'self',    -- self | typed | llm（§14 未来用）
  grade_detail_json TEXT,                        -- 判分细节（分数/反馈/模型/提示词版本）
  reviewed_at   TEXT NOT NULL,                   -- UTC
  review_day    TEXT NOT NULL,                   -- 用户本地复习日 YYYY-MM-DD（按 day_cutoff_hour）
  elapsed_ms    INTEGER,
  duration_days REAL,
  state_before  INTEGER NOT NULL,                -- 0=New 1=Learning 2=Review 3=Relearning
  interval_days REAL,
  stability     REAL,
  difficulty    REAL
);
CREATE INDEX idx_reviews_user_day ON reviews(user_id, review_day);
CREATE INDEX idx_reviews_card ON reviews(card_id, reviewed_at);

-- 授权（卡组共享；角色集合故意小）
CREATE TABLE deck_grants (
  deck_id       INTEGER NOT NULL REFERENCES decks(id) ON DELETE CASCADE,
  user_id       INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  role          TEXT NOT NULL,                   -- owner | editor | reader
  created_by    INTEGER REFERENCES users(id),
  created_at    TEXT NOT NULL,
  PRIMARY KEY (deck_id, user_id)
);

-- 分享链接（给"不想注册"的人：只读浏览，可设口令与过期，可即时撤销）
CREATE TABLE share_links (
  token         TEXT PRIMARY KEY,
  deck_id       INTEGER NOT NULL REFERENCES decks(id) ON DELETE CASCADE,
  role          TEXT NOT NULL DEFAULT 'reader',
  password_hash TEXT,
  expires_at    TEXT,
  created_by    INTEGER NOT NULL REFERENCES users(id),
  created_at    TEXT NOT NULL,
  revoked_at    TEXT
);

-- 媒体（字节在本地文件系统，表里只存相对路径）
CREATE TABLE media (
  id            INTEGER PRIMARY KEY,
  sha256        TEXT NOT NULL UNIQUE,            -- 内容寻址，天然去重
  rel_path      TEXT NOT NULL,
  mime          TEXT NOT NULL,
  bytes         INTEGER NOT NULL,
  width INTEGER, height INTEGER,
  created_by    INTEGER REFERENCES users(id),
  created_at    TEXT NOT NULL
);

-- 用户级 API Key（外部系统/Agent 凭据；与网页会话并存）
CREATE TABLE api_keys (
  id            INTEGER PRIMARY KEY,
  user_id       INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  name          TEXT NOT NULL,
  prefix        TEXT NOT NULL,                   -- 展示用前缀（明文只在创建时返回一次）
  key_hash      TEXT NOT NULL UNIQUE,            -- sha256(明文)
  scopes        TEXT NOT NULL DEFAULT 'read',    -- 逗号分隔：read,write,review,admin
  expires_at    TEXT,
  last_used_at  TEXT,
  revoked_at    TEXT,
  created_at    TEXT NOT NULL
);
CREATE INDEX idx_api_keys_user ON api_keys(user_id);

-- 后台任务（参数优化等长任务；web 触发、子进程执行）
CREATE TABLE jobs (
  id            INTEGER PRIMARY KEY,
  kind          TEXT NOT NULL,                   -- optimize | ...
  target_id     INTEGER,
  status        TEXT NOT NULL,                   -- queued | running | succeeded | failed
  stage         TEXT,                            -- read_logs | training | writing
  log_tail      TEXT,
  result_json   TEXT,
  error         TEXT,
  created_at    TEXT NOT NULL,
  started_at    TEXT,
  finished_at   TEXT
);

-- 审计（谁在什么时候改了什么）
CREATE TABLE audit_log (
  id            INTEGER PRIMARY KEY,
  user_id       INTEGER REFERENCES users(id),
  api_key_id    INTEGER REFERENCES api_keys(id),
  action        TEXT NOT NULL,                   -- user.create | deck.share | note.update | settings.update | ...
  target_type   TEXT, target_id INTEGER,
  detail_json   TEXT,
  created_at    TEXT NOT NULL
);
```

每日新卡/复习配额**不建计数表**：从 `reviews` 按 `(user_id, review_day)` 聚合得到，避免"计数与日志不一致"的经典 bug。

### 2.3 双库兼容约定（PostgreSQL + SQLite）

GORM 屏蔽了大部分差异，但以下几条必须人工守住：

- 不用任何 PG 专有类型：不写 `jsonb` / `serial` / `array`；JSON 一律存 `TEXT`，在 Go 侧序列化。
- 主键统一 `uint64` 自增；时间统一 `time.Time`（UTC 存储）。
- 冲突处理用 GORM 的 `clause.OnConflict`（两库都会生成各自正确的 SQL），不要手写 `ON CONFLICT` 方言。
- 分页统一 `LIMIT/OFFSET`；不要用 PG 的 `ILIKE`，模糊匹配用 `LOWER(col) LIKE`。
- 布尔用 Go `bool`（GORM 在两库分别落成 `boolean`/`numeric`，行为一致）。
- **AutoMigrate 只做增量**（加列/加索引）；破坏性变更（改类型、删列、加非空约束）必须写一次性迁移函数并在启动时按 schema 版本执行。
- 部分索引：SQLite 与 PG 语法都支持 `WHERE` 子句的索引，可用；但不要依赖 PG 特有的表达式索引。

---

## 3. 调度引擎（FSRS）

### 3.1 依赖

- `github.com/open-spaced-repetition/go-fsrs/v4` **v4.0.0**（MIT，算法版本 **FSRS v6**，权重 21 维）。已核实的 API：
  - `fsrs.NewFSRS(fsrs.DefaultParam())`；`(*FSRS).Repeat(card, now)`（预览四档）、`Next(card, now, grade)`、`Retrievability(card, now)`、`Reschedule(card, reviews, opts)`、`Forget(card, now, resetCount)`、`Rollback(card, log)`、`MemoryState(history, start)`。
  - 低层 `Parameters`：`NextState(state, retention, days, grade)`、`ApplyFuzz(interval, elapsedDays, enable)`、`NewBasicScheduler` / `NewLongTermScheduler`。
  - `DefaultWeights()`；`ConvertV5Weights` / `ConvertV45Weights` / `MigrateWeights`。
  - 错误是带 `Code` 的类型化 `*fsrs.Error`（`errors.Is/As` 可用）。
- 服务只做三件事：**持久化状态、构造队列、把评分喂给调度器**。

### 3.2 状态机

```
new ──(首次评分)──> learning ──(走完 learning_steps 且非 Again)──> review
                      ↑                                            │
                      └──────────── Again（失败）────────── relearning
```

- `learning_steps` 默认 `1m,10m`，`relearning_steps` 默认 `10m`；可配空串＝关闭学习步骤（新卡直接进长间隔排程）。
- `Again` 回到第一步；`Hard/Good/Easy` 在学习阶段推进步骤。
- **界面必须提醒"忘了就点 Again，别点 Hard"**：官方手册把 Hard 误用列为 FSRS 表现变差最常见的原因之一。

### 3.3 队列构建

按优先级出队：

1. **学习卡**：`state in (learning, relearning) AND due_at <= now`
2. **到期复习卡**：`state = review AND due_at <= now`，排序可选：
   - `按 retrievability 升序`（默认，最可能忘的先来）：`Retrievability()` 需在内存算，所以取一批（如 200 张）后排序
   - `按 due_at 升序`（经典顺序）
3. **新卡**：`state = new`，数量 = `min(每日新卡上限 − 今日已引入, 剩余)`；顺序默认 `随机`（避免永远只背开头几张），可选按创建顺序。

卡组级配置：`new_per_day`（默认 20）、`reviews_per_day`（默认 200，0=不限）。

**跨天与复习日**：`review_day` = 把"用户本地时间 − `day_cutoff_hour`"取日期（默认 04:00，凌晨刷的算前一天）。`due_at` 一律存 UTC。

### 3.4 评分提交（幂等与一致性）

```
POST /api/v1/review  { card_id, rating, expected_version, elapsed_ms }
  → 单事务：
      1. 取当前 state 并加行锁（GORM clause.Locking{Strength:"UPDATE"}；SQLite 靠单写连接串行化）
      2. 校验 state.version == expected_version，否则 409（客户端重放/双开窗口的安全网）
      3. 判分/自评来源记入 grade_source（self | typed | llm，见 §14）
      4. 调 s.Next(card, now, rating) 得新状态
      5. UPSERT card_states（version + 1）
      6. INSERT reviews（rating 1–4、state_before 0–3、interval、stability、difficulty、elapsed_ms）
  → 返回：新状态 + 下一张卡（省一次往返）
```

- `reviews` 只增不改。**撤销（Undo）**用 `Rollback(card, log)` 恢复状态，并删除最后一条日志（同时写一条 `audit_log` 说明）。
- `rating` 与 `state_before` 用**整数**（1–4 / 0–3），与 FSRS 生态的复习日志约定一致，将来接优化器零转换。
- `reviews` 字段从第一天就写全 —— 它是参数优化的唯一燃料，缺字段永远补不回来。

### 3.5 参数与优化器

- **默认**：`weights_json = NULL` → `DefaultWeights()`；用户唯一要理解的旋钮是 **目标保留率**（默认 0.90）。依据（官方手册）：desired retention 是 FSRS "最重要的设置"，默认 90%，**高于 90% 工作量增长很快，高于 97% 会变得难以承受** —— 这句话要写在滑杆旁边。
- **优化器的现实（已核实）**：`go-fsrs` 的 README 列了 `go-fsrs/optimizer`（*in development*）与 `go-fsrs/simulator`（*planned*），但截至 2026-10-01，其 `main` 与 `optimizer` 分支**都没有这两部分代码**（optimizer 分支仅多一个 `.idx/dev.nix`）——那是路线图，不是可用实现。现成可用的是：
  - Python：`open-spaced-repetition/fsrs-optimizer`（PyPI `FSRS-Optimizer`，定义了标准 review-log schema：`card_id / review_time(ms,UTC) / review_rating(1-4) / review_state(0-3) / review_duration / timezone / day_start`）
  - Rust：`fsrs-rs`（BSD-3，含优化器 API；仓库只有 `examples/optimize.rs`，**无现成 CLI**，Anki 内部即用它做优化）
- **本项目做法**：Web 触发的优化任务通过**子进程**调用一个**优化器适配器**（独立小工具，可执行文件与主程序一起分发；算法实现永不进 Go 代码）。
- **Web 触发（一切功能 web 优先）**：设置页 → `POST /presets/:id/optimize` → 建 job（202）→ **单 worker 串行执行**（已有任务则 409「正在优化中」）→ 前端轮询状态 → 完成就地刷新权重与对比。
  - **执行用子进程**（重新 exec 自身二进制或适配器）：① 训练 CPU-bound，必须能超时/取消（kill 进程组）；② 优化器不是 Go 库；③ 训练崩了不牵连 web 进程。
  - 进度只做**阶段级**（读取日志 → 训练 → 写回）+ 实时日志尾巴；不编造百分比。
  - 页面展示：权重来源（默认 / 已优化 + 时间 + 使用条数）、可用复习条数、不足时「还差 N 条」、失败原因、优化前后拟合对比（对应 Anki 手册的 "Check health" 思路）、一键回退默认权重。
  - 服务端门槛：复习记录不足（默认 < 500 条）直接拒绝并说明；不提供「每天优化」入口。
- **模拟器**（上游 *planned*）：将来做「工作量 vs 保留率」曲线帮助选目标保留率；当前用静态说明替代。
- **Reschedule**：改参数或优化后可选「按新参数重算现有到期日」（`Reschedule` 重放历史）；默认**不重算**，UI 明说「只影响未来的间隔」。

---

## 4. 身份与认证

设计目标：**通用**的用户体系 —— 内置账号为默认且唯一的必需组件，**OIDC 是可选接入项**，不绑定任何具体身份提供商。

### 4.1 内置用户系统（默认，开箱可用）

- 账号：`username`（唯一）+ `email`（唯一）+ `password_hash`（**argon2id**，参数可配；`golang.org/x/crypto`）。
- 角色：`admin` / `user`（本服务只有这两级；卡组级别的权限见 §5）。
- 状态：`active` / `disabled`（禁用后立即失效，会话作废）。
- **首个管理员**：首次启动走 web 引导页创建（避免"必须用 CLI 才能开始"）；同时支持环境变量 `BOOTSTRAP_ADMIN_EMAIL` 作为容器化部署的兜底。
- 个人设置（web）：显示名、界面语言、时区、复习日切点、密码修改。
- 不含密码的账号：纯 OIDC 用户可有 `password_hash = NULL`；管理员也可为本地账号强制"仅 OIDC 登录"（可选策略，默认允许两者）。

### 4.2 注册策略（管理员可配，web 切换）

| 策略 | 行为 |
|---|---|
| `open` | 任何人可自助注册（可选邮箱域名白名单） |
| `invite` | 仅凭邀请链接/邀请码注册（`invites` 表，一次性、可设过期与限定邮箱） |
| `closed` | 关闭自助注册，只允许管理员创建账号 |

- 默认 `closed`（自托管场景最安全）；策略与白名单在管理面板里改（§8.4）。
- 邀请链接生成/撤销走 web；不依赖邮件服务（链接由管理员直接发给对方），后续可选加 SMTP 自动发送。

### 4.3 会话与 CSRF

- 会话：服务端签名 cookie（HttpOnly + Secure + SameSite=Lax），签名密钥来自环境变量/密钥文件；登出即销毁。
- CSRF：所有非 GET 请求校验 token（表单注入 + `X-CSRF-Token` 头），与 SameSite 双保险。
- 登录限流与锁定：按账号 + IP 限速，连续失败递增延迟（防爆破）；失败与锁定写 `audit_log`。
- 密码策略：最小长度 + 常见弱密码拦截（内置小词表即可，不引外部服务）。

### 4.4 可选 OIDC 接入（通用，不特化厂商）

- **默认关闭**；管理员在系统设置里填参数开启，登录页随即出现第二个登录按钮。
- 只依赖标准 OIDC 发现文档：`issuer` + `client_id` / `client_secret` + scopes + claim 映射（`sub` / `email` / `name` / `email_verified` 的 claim 名可配），`redirect_uri = <base_url>/auth/oidc/callback`。任何符合规范的 Provider 都能接。
- Go 库选型：**`github.com/zitadel/oidc/v3`（v3.51.11，2026-10-01 仍在更新）** —— 它是 Go 生态最成熟的 OIDC 库之一，同时覆盖客户端与服务端实现，比 `coreos/go-oidc`（客户端专用、功能面更窄）更适合本项目后续可能的扩展。
- 流程安全：Authorization Code + **PKCE**，校验 `state` 与 `nonce`；issuer 走发现文档并缓存，配置变更后失效缓存。
- 内网/自托管注意事项写入实现注释：允许配置"解析到私网地址也能拉取发现文档"（很多自托管部署的 IdP 在内网），并把失败原因显示在管理面板的"测试连接"按钮上，而不是只写日志。

### 4.5 身份与内部用户的绑定

- 首次通过 OIDC 登录时：
  1. 若 `(provider, subject)` 已存在于 `identities` → 直接登录对应内部用户；
  2. 否则若 Provider 返回 `email_verified = true` 且邮箱与某个内部用户相同 → **自动绑定**（写一条 `identities`，记 `audit_log`）；
  3. 否则按注册策略：`open`/`invite` 下自动建号并绑定；`closed` 下拒绝登录并在页面提示"请联系管理员绑定账号"。
- 一个内部用户可有多个 `identities`（例如同时绑两个 IdP）；管理面板可查看并**解绑**。
- 内部用户邮箱修改不影响已绑定身份（绑定以 subject 为准，邮箱只是首次自动匹配的依据）。

### 4.6 密码与凭据存储

- 密码：argon2id（内存/迭代参数入库时记录算法版本，便于将来升级强度）。
- API Key：只存 `sha256`（见 §7.2）。
- 分享链接 token：随机 32 字节，只存哈希摘要。
- 所有敏感值（OIDC client secret、未来 LLM key）在 `settings` 表里**加密存储**（AES-GCM，主密钥来自环境变量），管理面板里只显示"已配置/未配置"，不回显明文。

---

## 5. 卡组共享与权限

**故意只做"共享"，不做"协作编辑"**（非目标见 §1）。

**角色（卡组级，只有三个）**

| 角色 | 看内容 | 自己复习（进度私有） | 改卡片内容 | 改卡组设置/授权 | 撤销共享 |
|---|---|---|---|---|---|
| owner | ✅ | ✅ | ✅ | ✅ | ✅ |
| editor（读写共享） | ✅ | ✅ | ✅ | ❌ | ❌ |
| reader（只读共享） | ✅ | ✅ | ❌ | ❌ | ❌ |

- 说明：**只要能看到卡组，就能用自己的账号复习**（进度是私有的，不会影响别人）；`reader` 与 `editor` 的差别只在"能不能改内容"。
- **撤销**：owner 随时可改角色或移除授权，**立即生效**（下一个请求即被拒）；已产生的个人进度保留在对方账号下（数据属其私有），但不再能访问该卡组。
- **分享链接**（`share_links`）：免注册只读浏览，可设口令与过期，可即时撤销、可一次撤销全部。
- **可见性**：`private`（仅授权者）/ `unlisted`（拿到链接可看）/ `public`（登录用户可见并可自取副本）。
- **克隆（fork）**：读者可把可见卡组复制到自己账号下，进度从零开始 —— 绕开"改别人的卡组"这类权限纠缠。
- 所有授权/撤销/分享操作写 `audit_log`（多人共用后"谁改了我的权限"必然会被问）。

---

## 6. 卡片内容与呈现

### 6.1 渲染管线

```
fields_json (Markdown + TeX)
   → goldmark 渲染 Markdown → HTML
   → bluemonday 白名单清洗（去掉 script/事件属性；允许 img/a/code/pre/table/span[class]）
   → 页面输出 HTML，浏览器端 MathJax 3 渲染 \( \) 与 \[ \]
```

- Markdown：`github.com/yuin/goldmark`；清洗：`github.com/microcosm-cc/bluemonday`。
- 公式：**MathJax 3，静态资源自托管**（`go:embed` 进二进制，不用公网 CDN）。配置 `inlineMath: [['\(','\)']]`、`displayMath: [['\[','\]']]`。
  - 选 MathJax 而非 KaTeX：公式覆盖更全，且与 Anki 生态、与多数笔记系统导出的 TeX 习惯一致。若列表页性能有问题，可只在复习页与详情页加载 MathJax，列表页显示纯文本。
- **安全**：卡片内容允许 HTML 子集，绝不允许脚本；媒体只允许白名单 mime。

### 6.2 内置题型（丰富 + 可扩展）

题型由**注册表**管理：每个题型实现统一接口，新增题型 = 新增一个实现文件 + 注册，**不改核心代码**；字段结构由题型自己定义与校验（存在 `notes.fields_json`）。

| 分类 | 题型 | 字段 | 生成 card | 判分方式 |
|---|---|---|---|---|
| 记忆类 | `basic` | front, back | 1 | 自评 |
| 记忆类 | `basic_both` | front, back | 2（正 + 反） | 自评 |
| 记忆类 | `cloze` | text（`{{c1::…}}` / `{{c1::…::提示}}`） | 每个 cloze 序号 1 张 | 自评 |
| 记忆类 | `list` | prompt, items[]（有序/无序） | 1（逐项揭示） | 自评 |
| 作答类 | `typed` | prompt, answer, 容差规则（大小写/空白/多答案/正则） | 1 | **机器判分** |
| 作答类 | `numeric` | prompt, value, 绝对/相对容差, 单位 | 1 | **机器判分** |
| 作答类 | `choice_single` | question, options[], answer | 1 | **机器判分** |
| 作答类 | `choice_multi` | question, options[], answers[] | 1 | **机器判分** |
| 作答类 | `true_false` | statement, answer | 1 | **机器判分** |
| 主观类（§14） | `short_answer` | prompt, reference（可选参考） | 1 | 先自评；未来 LLM 评分 + 反馈 |

- **题型接口（设计要点）**：`Validate(fields)` / `Cards(note)` / `Render(card, side)` / `Grade(input) (rating, detail, ok)`（可选，作答类实现）/ `PromptContext(note)`（可选，未来 LLM 评分用）/ `Label()`（i18n 显示名）。可选方法用可选的窄接口断言（`if g, ok := t.(Grader); ok`），不为将来预留大接口。
- 机器判分的分数 → FSRS 评分映射可配（默认：全对=Good、部分对=Hard、全错=Again；映射规则写在 preset 里，便于按题型调）。
- **数值/输入判分直接服务于"纯记忆映射"类内容**（如分数↔百分数互转），比纯自评更严格。
- 不引入用户自定义模板（非目标）：排版差异靠内置样式变量，不开放模板语言。

### 6.3 媒体（本地文件系统）

- 存储：**服务主机的本地目录**（默认 `data/media/`），布局 `media/<sha256[:2]>/<sha256>.<ext>`，内容寻址天然去重；**不接任何对象存储**。
- 读取：`GET /media/:id` 由服务代理，带 `ETag`（=sha256）与 `Cache-Control: immutable`；鉴权只需一处（有卡组访问权的登录用户）。
- 上传：白名单 mime（png/jpeg/webp/gif/mp3/ogg/m4a）+ magic bytes 校验；落盘用临时文件 + `rename`，避免半截文件。
- **上传上限由管理员在系统设置里配置**（单文件大小、允许的 mime 集合、可选单用户总量配额）；**不做服务端压缩/重编码**。
- 设置页显示媒体目录占用；磁盘接近阈值时在管理面板告警。

---

## 7. 对外集成：API Key + REST + 内置 MCP

### 7.1 原则

- 服务 = **卡片库 + 排程器 + 对外接口**。它**不认识任何外部笔记系统**，没有导入器。
- "从资料出卡"的逻辑全部在**外部**（脚本 / Agent / 任意系统），通过下面两个等价接口写入：
  - **REST API + 用户级 Key**（curl / 任意语言）
  - **内置 MCP server**（Agent 直接以工具调用）
- 两者共用同一套 service 层与鉴权判断；MCP 只做参数校验与包装 —— 不做成两套业务逻辑，否则规则必然漂移。

### 7.2 用户级 API Key

- 生成：`crypto/rand` 32 字节 → `fcard_<base64url>`；**明文只在创建时显示一次**；库里存 `sha256` 与展示前缀（如 `fcard_ab12…`）。
- 属性：`name`、`scopes`（`read` / `write` / `review` / `admin` 的子集）、可选过期时间、`last_used_at`、可即时撤销。
- **scope 语义**（本项目的权限模型就是这四档，不再往细里做）：
  | scope | 允许 |
  |---|---|
  | `read` | 读卡组、卡片、统计 |
  | `write` | 建/改/删卡片与卡组 |
  | `review` | 取到期卡、提交评分 |
  | `admin` | 管理 API Key、系统设置、用户（等价于管理员权限） |
  - 默认新建 key 只给 `read`；越权请求返回 403 并说明缺少哪个 scope。
  - 需要"只给某个卡组"时，用**卡组授权**（§5）限制该用户可见的卡组即可 —— key 继承其用户的权限，不额外做 key 级卡组白名单。
- 认证：`Authorization: Bearer fcard_…`；鉴权后落到 `user_id`，**权限边界与网页登录完全一致**。
- 限流：按 key 计数（默认 60 req/min），写入类接口更严。
- 审计：每次调用写 `audit_log`（带 `api_key_id`）—— "这张卡是谁建的"永远可查。

### 7.3 REST API（`/api/v1`）

JSON in/out；错误体统一 `{"error":{"code":"...","message":"..."}}`（`code` 稳定、可程序判断；`message` 本地化，随 `Accept-Language`）。

| 方法 | 路径 | scope | 说明 |
|---|---|---|---|
| GET | `/decks` | read | 我的卡组（按权限过滤） |
| POST | `/decks` | write | 建卡组 |
| GET | `/decks/:id/notes` | read | 卡片列表（分页、标签过滤、关键词搜索） |
| POST | `/decks/:id/notes` | write | **批量新增/更新**（幂等 `external_ref`；支持 `dry_run`；单次 ≤ 500） |
| PATCH | `/notes/:id` | write | 单卡更新 |
| DELETE | `/notes/:id` | write | 软删除 |
| GET | `/review/due?deck=:id&limit=n` | review | 取到期卡（含字段原文） |
| POST | `/review` | review | 提交评分（`card_id`,`rating`,`expected_version`,`elapsed_ms`） |
| GET | `/stats/summary` | read | 到期量/复习量/留存概要 |
| GET/POST/DELETE | `/keys[/:id]` | admin | API Key 管理 |
| GET | `/export?deck=:id&format=json\|csv` | read | 导出（`include_progress=1` 时含进度） |

- 幂等键 `external_ref` **由调用方**决定命名空间（例如某笔记系统用 `<system>:<docId>:<blockId>`），服务不做解释，只保证 `(deck_id, external_ref)` 唯一 → 同一批数据重复提交不会产生重复卡，且能反过来查"哪些来源还没建过卡"。
- 批量响应：`{created, updated, skipped, errors:[{index, reason}]}`；`dry_run=1` 只算不写。
- 更新已存在卡片时**保留所有用户的复习进度**（进度挂在 card 上，不在内容上）。

### 7.4 内置 MCP server（仅 HTTP，复用 API Key）

- 依赖官方 Go SDK：`github.com/modelcontextprotocol/go-sdk`（v1.8.0）。
- **挂载形态：只提供 HTTP**（`POST /mcp`，streamable HTTP）。**不提供 stdio 模式**（多用户服务没有"进程即身份"的语义，凭据统一走 key）。
- **认证：复用用户级 API Key**（`Authorization: Bearer fcard_…`），不引入第二套凭据体系。
- **工具按 key 的 scope 决定暴露哪些**：握手时按当前 key 过滤 `tools/list`，并在每次 `tools/call` 复查 scope —— 客户端看到的工具列表就是它真能用的集合。
  | 工具 | 需要 scope |
  |---|---|
  | `list_decks` / `search_notes` / `get_stats` / `export_deck` | read |
  | `create_notes`（支持 `dry_run`）/ `update_note` / `delete_note` | write |
  | `get_due_cards` / `submit_review` | review |
- **边界写死**：MCP 工具不封装业务逻辑，只做参数校验 + 调用与 REST 相同的 service 方法。同一条规则只改一处。
- 典型链路（完全在外部）：外部 Agent 读资料 → 生成问答对 → `create_notes(dry_run)` → 正式写入 → 人在网页上复习。服务只负责诚实记账。

### 7.5 导出与备份接口

- `GET /api/v1/export?deck=:id&format=json|csv[&include_progress=1]`
- 管理面板提供"全库导出"按钮（web 优先，不必用 CLI；CLI 仅作为自动化/运维的等价入口）。
- 导出**不含** Anki 兼容格式（见 §1 非目标）。

---

## 8. Web UI（templ + htmx）

**样式方案**：Tailwind CSS v4（**standalone CLI，免 Node**）+ Preline UI 的组件片段（片段直接进仓库、可随意改），交互件（开关/下拉/对话框）用 Web Awesome（web components，MIT Core）补。**明确不用 ElementUI / Ant Design / Bootstrap 这类"一眼模板化"的库**；目标是简洁、现代、不像后台模板。

### 8.1 页面清单

| 路由 | 页面 | 说明 |
|---|---|---|
| `/` | 今日 | 各卡组到期数/新卡数、总到期量、开始复习、连续打卡 |
| `/decks` `/decks/:id` | 卡组列表/详情 | 卡片表格（分页、搜索、标签过滤）、批量操作 |
| `/decks/:id/notes/:nid` | 卡片编辑 | 字段编辑 + 实时预览（htmx 局部刷新 + MathJax 重渲染） |
| `/review?deck=:id` | 复习 | 核心页（见 §8.2） |
| `/import` | 批量导入 | 粘贴/上传 JSON、dry-run 报告、确认写入（与 API 等价，给不想用 curl 的人） |
| `/export` | 导出 | 选卡组与格式 |
| `/stats` | 统计 | 见 §9 |
| `/settings` | 个人设置 | 语言、时区、复习日切点、密码、API Key 管理 |
| `/presets` | 调度预设 | 目标保留率、学习步骤、最大间隔、fuzz、权重（含"优化"按钮与状态） |
| `/sharing` | 共享管理 | 授权列表、分享链接、可见性、克隆 |
| `/admin/*` | 管理面板 | 见 §8.4 |
| `/login` `/register` `/invite/:token` `/auth/oidc/callback` | 认证 | 内置账号 + 可选 OIDC |

### 8.2 复习页交互（体验核心）

- **键盘**：`空格/Enter` 显示答案；`1/2/3/4` = Again/Hard/Good/Easy；`u` 撤销；`e` 编辑；`s` 暂停；`b` 埋藏（本日跳过）。
- **手机**：左右滑动 = 显示答案 / 评分（少量原生 JS，不引 SPA）；按钮点击区 ≥ 44px；禁用双击缩放与长按选中（`touch-action` / `user-select`），否则连续点击会触发系统菜单 —— 这是手机复习最常见的体验杀手。
- **预取**：评分响应里直接带下一张卡，避免"评分 → 跳转 → 加载"的延迟。
- **进度可见**：顶部显示 `今日剩余 N 张`、`本次已复习 M`。
- **作答类题型**：显示输入框/选项 → 提交 → 立即判分并显示正确答案与解析（判分结果进 FSRS）。
- **断网**：明确提示失败（不静默），已提交的评分不受影响。
- 复习中可展开 `extra`/`source_url`（核对出处）。

### 8.3 多语言（i18n）

- **首发语言：中文（zh-CN）+ 英文（en）**；默认跟随浏览器 `Accept-Language`，用户可在个人设置里固定；管理员可设站点默认语言。
- 实现：`github.com/nicksnyder/go-i18n/v2`（v2.6.1）+ YAML 语言包（`internal/i18n/locales/zh-CN.yaml`、`en.yaml`）；templ 模板从请求上下文取 translator；**所有用户可见文案（含错误提示、邮件/通知文本、题型显示名）都必须走语言包**，禁止硬编码字符串（加 lint 检查：模板里出现中文字面量就报错）。
- **API 与 MCP 错误**：返回稳定的 `code` + 本地化 `message`（按 `Accept-Language`），便于脚本判断与人类阅读。
- 日期/数字/相对时间用 `golang.org/x/text` 的本地化格式化。
- 新增语言 = 加一个 YAML 语言包（不需要改代码），并在管理面板可查看各语言完成度。
- **边界**：i18n 只管**用户可见文案**；**日志恒为英文**（见 §10.4 文本语言约定），不随语言切换，也只写英文 message，不从语言包取值。
- 项目文档双份：`README.md`（英文，一级公民）+ `README.zh.md`。

### 8.4 管理面板（web 优先，尽量不用 CLI）

| 区块 | 功能 |
|---|---|
| 用户管理 | 列表/搜索、创建用户、禁用/启用、重置密码、改角色、强制登出、删除、查看其卡组与用量 |
| 注册与邀请 | 注册策略切换（open/invite/closed）、邮箱域名白名单、邀请链接生成/撤销/查看使用情况 |
| 身份与 OIDC | OIDC 开关与参数、claim 映射、**"测试连接"按钮**（把失败原因显示在页面上）、已绑定身份列表与解绑 |
| 系统设置 | 站点名称、默认语言、上传大小上限与允许的 mime、媒体目录占用、全库导出/备份按钮 |
| 任务 | Job 列表（优化器等）、状态/进度/日志尾巴、取消 |
| 审计 | `audit_log` 检索（按用户/动作/时间/来源 key） |
| 健康 | 数据库连通与 schema 版本、磁盘占用、当前到期队列量 |
| API Key 总览 | 所有用户的 key 元信息（名称/前缀/scope/最后使用/状态）；**不显示明文、不可查看他人 key 的完整值** |

- 配置优先级：**环境变量（仅启动必需项，如密钥与 DSN）< `settings` 表（管理员可改，web 即时生效）**；每条设置标明来源，避免"改了没生效"的困惑。

### 8.5 PWA 与静态资源

- `manifest.webmanifest` + 图标 + `display: standalone`：手机可"添加到主屏幕"，像 App 一样启动。
- Service Worker：**只缓存静态资源外壳**（CSS/JS/字体/MathJax），不缓存答题数据、不做离线队列（与 §1 非目标一致）。
- 静态资源 `go:embed` 进二进制，带 hash 的 cache-busting 路径。

---

## 9. 统计与洞察

全部由 `reviews` + `card_states` 聚合而来，不引入额外数据源。

| 指标 | 口径 |
|---|---|
| 今日/近 7 日/近 30 日复习量 | 按 `review_day` 聚合 `count(*)` |
| 到期预测 | 按 `card_states.due_at` 分桶（今日/明日/7 日内/30 日内/更远/新卡未到期） |
| 留存率 | 按 `stability` 或 `interval` 分桶，统计"到期时首次评分不是 Again"的比例 |
| 记忆强度分布 | 用 `Retrievability()` 现算每张卡当前概率，分 10 档直方图 |
| 时间投入 | `reviews.elapsed_ms` 的日均/中位数 |
| 卡组维度 | 每卡组到期量、留存率、累计投入 |
| 标签维度 | 按 `notes.tags_json` 聚合（"哪块内容弱"的最快线索） |
| 学习曲线 | 每日新引入 vs 每日复习量（判断新卡速率是否超过消化能力） |
| 判分来源分布 | 按 `grade_source` 统计自评/机器判分/（未来）LLM 评分占比 |

图表用服务端生成 SVG 或轻量 JS（自托管）；第一版用纯 HTML/CSS 条形图 + 数字表，够用且零依赖。

---

## 10. 架构与技术选型

### 10.1 技术栈（版本已于 2026-10-01 核实）

| 层 | 选择 | 版本 | 理由 |
|---|---|---|---|
| 语言 | Go | 1.25+（开发机 1.27） | — |
| HTTP | `github.com/gin-gonic/gin` | v1.12.0 | 需求指定；htmx 只需路由与表单绑定 |
| 视图 | `github.com/a-h/templ` | v0.3.1020 | 需求指定；类型安全模板，编译产物可 embed |
| 交互 | htmx（自托管 JS） | vendor | 免前端构建链，服务端渲染为主 |
| ORM | `gorm.io/gorm` | v1.31.2 | 需求指定；AutoMigrate 省掉独立迁移工具；模型即业务模型 |
| 数据库 | `gorm.io/driver/postgres` / `gorm.io/driver/sqlite`（或纯 Go 的 `glebarez/sqlite`） | v1.6.3 / v1.6.0 | **同时兼容 PostgreSQL 与 SQLite** |
| 调度 | `github.com/open-spaced-repetition/go-fsrs/v4` | v4.0.0 | 官方 Go 实现，FSRS v6 |
| 身份 | `github.com/zitadel/oidc/v3`（OIDC 客户端）+ `golang.org/x/crypto`（argon2id） | v3.51.11 / v0.57.0 | Go 生态最成熟的 OIDC 库；内置账号用标准库 + x/crypto |
| i18n | `github.com/nicksnyder/go-i18n/v2` | v2.6.1 | YAML 语言包，中英起步、可扩展 |
| 对外集成 | `github.com/modelcontextprotocol/go-sdk` | v1.8.0 | 官方 MCP 实现（HTTP 传输） |
| Markdown | `github.com/yuin/goldmark` | v1.8.6 | 标准选择、可扩展 |
| 清洗 | `github.com/microcosm-cc/bluemonday` | v1.0.27 | 标准选择 |
| 公式 | MathJax 3（自托管） | 3.x | 与主流笔记/Anki 的 TeX 习惯一致 |
| UI 样式 | Tailwind CSS v4（**standalone CLI，免 Node**）+ Preline UI 片段；交互件用 Web Awesome | 最新 | 现代观感、不被组件库绑架、与 htmx 兼容 |
| 校验 | `github.com/go-playground/validator/v10`（可选） | v10.30.5 | 表单/API 入参校验 |
| 日志 | 标准库 `log/slog` | — | 少依赖 |

**选型原则**：**低代码复杂度、尽量使用成熟开源库而不是重复造轮子**。判据是"这个轮子别人是否已经造好且维护良好"—— 是就用它（GORM、templ、htmx、go-fsrs、MCP SDK、Tailwind/Preline…）；否才自己写（队列构建、幂等导入、题型注册表、判分这些是本项目的核心价值，且没有现成可用）。

### 10.2 进程形态与子命令

- **单二进制**：`flashcard`，子命令：
  - `serve`（HTTP 服务，默认；含 Web + REST + `/mcp`）
  - `schema sync`（显式跑 AutoMigrate；`AUTO_MIGRATE=1` 时启动也会跑）
  - `export --all --out backup.json`（备份/迁移；管理面板有等价按钮）
  - `optimize --job N`（**由 web 侧建 job 后 fork 出来执行**，见 §3.5，不是给人手敲的常规命令）
  - `version`
- 模板、静态资源（含 MathJax、Tailwind 产物）、语言包全部 `go:embed` → 分发物 = 一个二进制 + 数据库（+ `data/media/`）。
- 单进程；批量导入在请求内完成；长任务（参数优化）走 job + 子进程。

### 10.3 目录结构（建议）

```
flashcard/
├── AGENTS.md                 # 开发指引（英文）：硬性约定、完成标准、任务清单与进度统计
├── DESIGN.md                 # 本文档
├── README.md / README.zh.md  # 英文为一级公民，中文版并列
├── LICENSE
├── go.mod                    # module <module-path>
├── cmd/flashcard/main.go     # 子命令入口
├── internal/
│   ├── config/               # 环境变量 + settings 表读取与优先级
│   ├── store/                # GORM 模型 + 各业务 Store（集中封装数据访问）
│   ├── schedule/             # go-fsrs 封装：状态机、队列构建、评分提交
│   ├── cardtype/             # 题型注册表与各题型实现（§6.2）
│   ├── auth/                 # 内置账号、会话、CSRF、OIDC、API Key、权限判定
│   ├── media/                # 本地文件存储：去重、落盘、代理读取
│   ├── api/                  # /api/v1 handler（批量导入、dry_run）
│   ├── mcp/                  # 内置 MCP server（工具定义 → 调 service，按 scope 过滤）
│   ├── i18n/                 # locales/{zh-CN,en}.yaml + 加载与 translator
│   └── web/                  # gin 路由 + handler + templ 视图 + static/
│       ├── handlers/         # 含 admin/*
│       ├── views/            # *.templ
│       └── static/           # htmx.js, mathjax/, tailwind.css, manifest, sw.js
└── test/                     # 集成测试（SQLite 内存/临时库；可选 PG）
```

### 10.4 关键实现约定

- **工程风格**：朴素单体、按业务包组织、优先具体类型、入口显式装配依赖；**业务模型 = GORM 模型 = JSON/CSV 模型**（不做 domain/entity/DTO 三层映射）；只在确有多种实现或替换需求时才引入接口，且**最小接口定义在消费方**；用具体 Store 集中封装 GORM 访问，只有复杂流程才加少量 Service；阻塞操作一律接收 `context.Context`。
- **事务边界**：评分提交（state upsert + review insert）单事务；批量导入按批（默认 200 条/事务）提交，失败可续。
- **双库兼容**：见 §2.3。
- **时间**：存储一律 UTC；展示与"复习日"计算套用户时区/切点。
- **测试**：`schedule`（队列、状态流转、幂等、乐观锁）与 `cardtype`（判分）是核心单测目标；handler 用 `httptest` + 内存/临时 SQLite（不 mock 数据库）；CI 可选再跑一遍 PostgreSQL。
- **文本语言约定（硬性，2026-10-01 定）**：
  - **日志一律英文**：`slog` 的 message 与结构化字段、启动/关闭信息、任务与子进程输出、CLI 诊断输出、`panic` 与内部 error 文案 —— 全部英文，**不本地化、不随语言切换**（便于检索、检索式排错与开源协作）。
  - **代码注释一律中文**：包/函数文档注释与关键实现说明都用中文；注释解释"为什么"，不复述代码。
  - **面向用户的文案走语言包**（中/英，见 §8.3）：界面文字、按钮、表单校验提示、API/MCP 的 `message` 字段属于此列；`code` 字段用英文常量。
  - 一句话判据：**给机器和开发者看的 → 英文；给读代码的人看的 → 中文；给最终用户看的 → 语言包**。日志 message、语言包键名、错误 `code` 都用英文命名，避免中英混杂的标识符。

- **构建与校验**：`go build ./... && go vet ./... && gofmt -l . && go test ./...`；模板与语言包缺失键在测试里断言（避免出现未翻译文案）。
- **脱敏**：仓库内任何文件（示例配置、脚本、测试夹具、文档）不得出现真实域名、主机名、内网地址、个人邮箱、真实凭据；示例统一用 `example.com` / `localhost` 之类的占位值，并在 CI 里加一条关键词扫描。

---

## 11. 安全与隐私

| 面 | 措施 |
|---|---|
| 密码 | argon2id（参数可配、算法版本入库）；强度检查 + 弱密码小词表；登录限流与递增延迟 |
| 会话 | 服务端签名 cookie（HttpOnly/Secure/SameSite=Lax）；登出销毁；改密码/禁用用户时作废其全部会话 |
| CSRF | 非 GET 请求校验 token（表单 + `X-CSRF-Token`），与 SameSite 双保险 |
| OIDC | Authorization Code + PKCE；校验 `state`/`nonce`；issuer 走发现文档；绑定策略见 §4.5；解绑与绑定写审计 |
| API Key | 只存 sha256；明文只显示一次；scope 最小化（默认 `read`）；按 key 限流；撤销即时生效；调用写审计 |
| 分享链接 | 随机 32 字节 token，只存摘要；可设口令与过期；可批量撤销 |
| XSS | 卡片内容经 bluemonday 白名单清洗；模板默认转义；**禁止**用户自定义脚本/模板 |
| 上传 | mime 白名单 + magic bytes 校验（不只信扩展名）；大小上限管理员可配；路径由 sha256 生成，不含用户可控字符串 |
| 越权 | 每个 handler 第一行做 `requireRole(deckID, userID, role)`；单测覆盖"reader 试图改卡""越权导出"等反面用例 |
| 敏感配置 | OIDC secret、未来 LLM key 在 `settings` 表 AES-GCM 加密存储（主密钥来自环境变量）；界面只显示"已配置/未配置" |
| 审计 | 用户、权限、设置、key、导入导出等写 `audit_log`（多人共用后这是必需品） |
| 隐私 | 不引任何第三方 JS/字体/CDN（MathJax、htmx、字体全部自托管）；不做遥测；数据只在本机 |
| 脱敏 | 见 §10.4 最后一条（仓库级要求） |

---

## 12. 里程碑

每个里程碑可独立交付并真实使用；后续步骤不推翻前面的设计。

| # | 内容 | 交付物 | 验收标准 |
|---|---|---|---|
| **M0** | 骨架 | `go mod init`、config（环境变量 + settings 表优先级）、gin 启动、GORM 模型 + AutoMigrate（§2.2 最小集）、i18n 骨架、`/healthz`、templ + Tailwind 构建接入 | `go build/vet/test` 全绿；PG 与 SQLite 两种 DSN 都能建表启动；中英两套语言包能切 |
| **M1** | 身份与用户 | 内置账号注册/登录/登出/改密、首个管理员引导页、注册策略（open/invite/closed）+ 邀请、会话与 CSRF、个人设置（语言/时区/复习日切点） | 全新实例能在浏览器里完成"建管理员 → 邀请用户 → 用户注册登录"；禁用用户后其会话立即失效；CSRF 反面用例单测通过 |
| **M2** | 卡组与卡片 | deck/note/card CRUD、题型注册表 + 记忆类题型（basic/basic_both/cloze/list）、渲染（Markdown + MathJax）与预览 | 网页上建一个卡组并手工录入公式卡，能正确渲染；新增一个题型只需加一个实现 + 注册（不碰核心） |
| **M3** | 复习闭环 | 队列构建、go-fsrs 接入、评分提交（乐观锁）、Undo、暂停/埋藏、每日上限、作答类题型 + 判分（typed/numeric/choice/true_false） | 真实复习一轮：到期日随评分变化符合预期；重复提交同一评分被 409 挡；队列优先级正确（单测）；输入答案能判对判错并进 FSRS |
| **M4** | 对外集成 | API Key 管理（个人设置页）、`/api/v1`（批量建卡含 dry-run 与幂等、取到期、提交复习、导出）、**内置 MCP server**（`/mcp`，按 scope 过滤工具） | 用 curl 与一个真实 MCP 客户端分别完成：建卡 → 取到期 → 提交复习 → 读统计；同批数据重复提交不产生重复卡；越权/过期 key 被拒（单测覆盖） |
| **M5** | 共享与权限 | 三角色授权、分享链接、可见性、克隆、撤销即时生效 | 第二个用户在只读卡组里能复习但不能改卡；撤销后其访问立即失败；越权用例单测通过 |
| **M6** | 管理面板与系统设置 | 用户管理、注册/邀请、OIDC 开关与测试连接、上传上限等系统设置、审计检索、健康页、全库导出 | 全程浏览器完成，不需要 CLI；设置改动即时生效且标明来源；OIDC 配置错误时页面上能看到原因 |
| **M7** | 统计 | 到期预测、留存、时间投入、标签维度、判分来源分布、打卡 | 数字与手写 SQL 对拍一致 |
| **M8** | 移动体验 + PWA + i18n 收口 | 手势、禁缩放/长按、预取、manifest + SW、语言包完成度检查 | 手机浏览器连刷 50 张无卡顿、无系统菜单误触；主屏图标启动为独立窗口；界面无硬编码文案 |
| **M9** | 参数优化 | `optimize` job（web 触发 + 子进程适配器）、预设页展示/回退、门槛提示、前后拟合对比 | 达阈值后优化能产出 21 维权重并落库；不足阈值明确拒绝；可一键回退 |
| **M10** | （未来）LLM 接入 | 见 §14 | 见 §14 |

任务粒度拆分、每项验收标准与进度统计口径见 `AGENTS.md` §5–§6（任务 ID 形如 `M3-2`，完成度按里程碑统计）。

**排序理由**：M1–M3 完成后即可开始真实复习（项目的第一价值点）；M4 让外部 Agent 能写卡；M5/M6 把"给朋友用"和"管理员自助"补齐；M9 属于锦上添花，必须等有足量复习日志。

---

## 13. 开放问题

| # | 问题 | 选项 | 建议 |
|---|---|---|---|
| 1 | **项目名 / 模块路径** | 待定（暂用 `flashcard` / `<module-path>` 占位） | 名字定下来后全局替换占位符即可 |
| 2 | **开源许可证** | MIT / Apache-2.0 / AGPL-3.0 | 若希望别人能自由集成（含闭源部署）→ MIT/Apache-2.0；若担心被第三方做成 SaaS 而不回馈 → AGPL-3.0（注意与本项目"供自托管"的定位一致）。**需要你拍板** |
| 3 | 是否要 TOTP 二次验证 | 要 / 不要 / 后期 | 本地账号体系下，TOTP 是廉价的安全加分项；建议列入后期（不进 M1） |
| 4 | 邀请与找回密码是否走邮件（SMTP 配置项） | 纯链接（管理员转发）/ 集成 SMTP 自动发送 | 先纯链接（零依赖）；SMTP 作为后期可选项 |
| 5 | 站点默认语言 | 中文 / 英文 / 跟随浏览器 | 站点默认 `zh-CN`，个人可覆盖，登录前按 `Accept-Language` |
| 6 | 媒体目录是否需要"每用户配额" | 只要单文件上限 / 单文件 + 单用户总量 | 先只做单文件上限（管理面板可调），配额等有人滥用再加 |
| 7 | 是否要"卡组导入/导出模板示例"（给外部 Agent 看的 JSON Schema） | 只要文档 / 提供 JSON Schema 文件 | 提供 `schema/note-import.schema.json`，外部工具可直接校验 —— 成本低、对 Agent 友好 |

---

## 14. 未来规划（本期不实现，但设计需为它留口子）

> 这一节的目的不是承诺功能，而是**确保现在的数据模型与代码结构不会被它推倒**。落进设计的具体口子见 §14.4。

### 14.1 内置 LLM 接入

- 用途：主观题评分与反馈、复习建议、可能的出卡辅助（出卡仍以外部为主）。
- 接入方式：**OpenAI 兼容接口**为主（`base_url` + `api_key` + `model`），管理员在系统设置里配置；支持两种凭据模式：**系统级共享 key** 或 **用户自带 key（BYOK）**。
- 参数：超时、重试、并发上限、每月调用上限（成本闸门）、模型名与温度；调用记录写审计（谁、哪张卡、耗时、token 估算）。
- 隐私：只发送"当前卡片 + 用户答案 + 该卡绑定的参考资料片段"，绝不发送整库；提供"关闭 LLM 功能"的总开关（默认关闭）。

### 14.2 主观题 LLM 评分（含知识库参考）

- 场景：`short_answer` 类题型（例如"解释这个公式的适用条件"）先由用户作答，再由 LLM 打分与反馈。
- 卡片可绑定**固定资料/知识库**（deck 级默认 + note 级覆盖）作为评分依据；知识库内容为文档分块（chunk），检索方式先用关键词/全文，量大后再上向量检索（这也是选 PostgreSQL 的一个额外理由：可挂 `pgvector`）。
- 流程：提交答案 → 组装上下文（题目 + 参考答案 + 检索到的片段 + 评分细则）→ LLM 返回结构化结果（分数 0–100、判定理由、改进建议）→ **映射到 FSRS 四档评分**（阈值可配）→ 写入 `reviews`（`grade_source = 'llm'`，`grade_detail_json` 存分数/反馈/模型/提示词版本/耗时）。
- 交互：LLM 调用走**异步**（提交后进入队列，页面轮询或稍后刷新），**不能阻塞复习流程**；结果未回来时可先按"自评"继续。
- 可解释性：卡片详情页展示历史评分与反馈，便于对比"自评 vs LLM 判定"的差异。

### 14.3 其他候选（更远期）

- 出卡辅助：外部 Agent 之外的"在服务内从参考资料生成草稿卡"（仍以"人审后入库"为前提）。
- 语音题型（发音/听力）、图片遮挡（image occlusion）题型。
- 多用户学习小组：共享学习计划与进度看板（不涉及内容协作编辑）。

### 14.4 现在就留的口子（不实现，但结构上支持）

1. **评分来源字段已建**：`reviews.grade_source`（`self | typed | llm`）与 `grade_detail_json` —— 将来接 LLM 评分时不需要数据迁移。
2. **题型接口预留可选能力**：`Grade()`（机器判分，已在作答类题型的计划内）与 `PromptContext()`（为 LLM 评分提供题目上下文/参考答案）、`ReferenceRefs()`（该卡依赖的知识库片段）。用可选窄接口断言，将来加 LLM 评分不改核心提交流程。
3. **`notes.reference_refs` 字段已留**（知识库片段引用），先不建 UI 与检索。
4. **`settings` 表用 JSON 值** + 敏感值加密通道（§11）：LLM 端点/密钥属于"新增一条设置"，不需要改 schema。
5. **`jobs` 表是通用的**：LLM 批量评分/批处理复用同一套"web 触发 + 子进程/后台执行 + 页面轮询"的机制。
6. **异步评分路径预埋**：评分提交接口区分"同步判分（自评/机器判分）"与"异步判分（未来 LLM）"，数据结构上先支持 `pending` 状态的判分请求（本期只有同步实现）。

---

## 15. 依据与参考（均已核实，2026-10-01）

| 事实 | 来源 |
|---|---|
| go-fsrs v4.0.0，MIT，FSRS v6，21 维权重，`Repeat/Next/Retrievability/Reschedule/Forget/Rollback`，`EnableFuzz`，`NextState(state, retention, days, grade)` | `github.com/open-spaced-repetition/go-fsrs` README 与 releases；proxy.golang.org |
| **`go-fsrs` 的优化器与模拟器尚无代码**：README 列了 `go-fsrs/optimizer`(in development) / `go-fsrs/simulator`(planned)，但 `main` 与 `optimizer` 分支的仓库树里都没有对应文件（optimizer 分支仅多 `.idx/dev.nix`） | GitHub API：两分支 git tree 对比 |
| 优化器现成实现：`fsrs-optimizer`（Python，定义标准 review-log schema：`card_id/review_time(ms,UTC)/review_rating(1-4)/review_state(0-3)/review_duration/timezone/day_start`）；`fsrs-rs`（Rust，含优化器 API，仅有 `examples/optimize.rs`，无现成 CLI） | `open-spaced-repetition/fsrs-optimizer` README；`open-spaced-repetition/fsrs-rs` 仓库树 |
| Anki 手册：desired retention 是 FSRS 最重要的设置（默认 90%，>90% 工作量增长快、>97% 难以承受）；优化器需数百条复习、每月一次即可；Hard 误用是 FSRS 表现差的常见原因 | docs.ankiweb.net/deck-options.html（FSRS / FSRS Parameters / Optimize） |
| Anki 公式渲染：MathJax 开箱即用，行内 `\(…\)`、独立 `\[…\]`；LaTeX 图像模式需手动开启且有安全风险、官方不推荐 | docs.ankiweb.net/math.html |
| `.apkg` 结构：zip 内含 `collection.anki2`/`.anki21`/`.anki21b`（最新版 zstd 压缩 + schema V18）+ protobuf `meta`，媒体清单旧版 map、新版数组 | `ankitects/anki` → `rslib/src/import_export/package/meta.rs` |
| Go 依赖版本：gin v1.12.0 / templ v0.3.1020 / gorm.io/gorm v1.31.2 / driver/postgres v1.6.3 / driver/sqlite v1.6.0 / glebarez/sqlite v1.11.0 / go-fsrs v4.0.0 / **zitadel/oidc/v3 v3.51.11** / x/crypto v0.57.0 / go-i18n v2.6.1 / modelcontextprotocol/go-sdk v1.8.0 / goldmark v1.8.6 / bluemonday v1.0.27 / validator v10.30.5 | proxy.golang.org `@latest` |
| 前端可选件：Tailwind CSS v4 提供**免 Node 的 standalone CLI**；Preline UI 为开源 Tailwind 组件库（MIT + Fair Use 双许可、仍在维护）；Web Awesome（Shoelace 后继；Shoelace 已于 2.20.1 停更）Core 为 MIT | tailwindcss.com/docs/installation/tailwind-cli；preline.co/license；webawesome.com/license |

---

## 附：需求追溯

| 需求 | 落点 |
|---|---|
| 网页优先、无桌面客户端、少用 CLI | §1、§8（templ + htmx + PWA）、§8.4（管理面板 web 化） |
| 多用户、各自独立进度 | §2.1（`card_states` 主键含 user_id）、§5 |
| 与朋友共用（非技术用户、手机） | §5 共享与分享链接、§8.2 手机交互、§8.5 主屏安装 |
| 卡组共享（只读/读写/撤销） | §5（三角色 + 分享链接 + 克隆 + 即时撤销） |
| 通用身份 + 可选 OIDC | §4（内置账号默认、OIDC 可选、`zitadel/oidc` 库、身份绑定） |
| 注册策略由管理员控制 | §4.2 + §8.4 |
| FSRS 排程 | §3 |
| 答题严格判分（数值/输入） | §6.2 作答类题型 + §6.2 判分映射 |
| 题型丰富且易扩展 | §6.2（注册表 + 内置 10 种题型） |
| 外部系统/Agent 写入 | §7（API Key + REST + 内置 MCP，仅 HTTP、按 scope 过滤工具） |
| 多语言（中/英） | §8.3（go-i18n + YAML 语言包 + 无硬编码文案） |
| 管理面板（用户/系统设置） | §8.4 |
| 上传限制管理员可配、不压缩 | §6.3 + §8.4 |
| 不兼容 Anki 格式 | §1 非目标（含依据）、§7.5 |
| 不做协作编辑 | §1 非目标、§5（只做共享 + 克隆） |
| 扁平卡组（无树） | §2.2（`decks` 无父子关系）+ §5 |
| 未来 LLM 评分与知识库 | §14（含 §14.4 现在就留的口子） |
| 开发文本语言（英文日志 / 中文注释 / 用户文案走语言包） | §10.4、§8.3 |
| 自托管、数据主权、脱敏开源 | §1、§11、§10.4（脱敏要求） |

---

## 附二：已定决策摘要（2026-10-01 评审结论）

一眼可查的定案清单（细节见对应章节）：

| # | 决策 | 落点 |
|---|---|---|
| 1 | 前端多语言：首发中文 + 英文，语言包驱动、禁止硬编码文案 | §8.3 |
| 2 | 项目正式名待定，全文用 `flashcard` / `<module-path>` 占位 | §13 #1 |
| 3 | 身份体系**通用化**：内置账号为默认，**OIDC 可选接入**、与内部用户绑定；OIDC 库用 Go 生态最成熟的 `zitadel/oidc`（不特化任何 IdP） | §4 |
| 4 | 设计文档不含部署与运维章节（属私有信息，不进开源仓库） | 全文 |
| 5 | 已在本地初始化 git 仓库（`main`，首个提交含 DESIGN.md / .gitignore / .env.example） | 仓库 |
| 6 | 仓库内一切文件脱敏，作为开源项目开发；本地临时文件与测试配置进 `.gitignore` | §10.4、`.gitignore` |
| 7 | 数据库：**PostgreSQL 为默认部署库**，SQLite 保留为单机/开发模式；两套备份与恢复步骤写进 README | §2.3、§10.1 |
| 8 | 不做协作编辑，只做共享：只读 / 读写 / 随时撤销（+ 分享链接 + 克隆） | §5 |
| 9 | 固定题型（无用户自定义模板），内置题型尽量丰富且注册表化、易扩展 | §6.2 |
| 10 | 注册策略由管理员控制：`open` / `invite` / `closed`（+ 邮箱域名白名单） | §4.2 |
| 11 | 扁平卡组，不做卡组树（用标签） | §2.2、§5 |
| 12 | UI：Tailwind CSS v4（免 Node standalone CLI）+ Preline 片段，交互件用 Web Awesome；不用模板化组件库 | §8 |
| 13 | 参数优化适配器用 **Rust bin**（调 `fsrs-rs`），算法不进 Go 代码 | §3.5 |
| 14 | API Key scope 四档：`read` / `write` / `review` / `admin`（不做 key 级卡组白名单，用卡组授权限制） | §7.2 |
| 15 | MCP **只提供 HTTP**、**复用用户 API Key**、**按 key scope 过滤工具**（无 stdio） | §7.4 |
| 16 | 上传上限由管理员在系统设置里配置；**不做服务端压缩** | §6.3、§8.4 |
| 17 | 完善的管理员面板：用户管理、注册策略、OIDC 配置、系统设置、审计、任务——全部 web 优先，尽量不使用 CLI | §8.4 |
| 18 | 未来规划（本期不实现但留口子）：内置 LLM 接入、主观题 LLM 评分、卡片绑定知识库参考 | §14 |

**未决（需要拍板）**：项目名与模块路径、开源许可证，以及 §13 的其余可选项。
