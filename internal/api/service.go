package api

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	"gorm.io/gorm"

	"git.nite07.com/nite/engram/internal/auth"
	"git.nite07.com/nite/engram/internal/cardtype"
	"git.nite07.com/nite/engram/internal/schedule"
	"git.nite07.com/nite/engram/internal/store"
)

// 本文件是 REST 与内置 MCP 共用的 service 层（DESIGN.md §7.1、§7.4：两者只做参数
// 校验与包装，真实业务规则必须只有一份实现）。REST handler 与 MCP 工具都调用这里的
// 方法，保证同名操作产出完全一致。

// ServiceError 携带稳定错误 code 与 HTTP 语义状态码，供 REST 包壳与 MCP 错误结果共用。
type ServiceError struct {
	Status  int
	Code    string
	Message string
}

func (e *ServiceError) Error() string { return e.Message }

// newServiceError 构造一个 ServiceError。
func newServiceError(status int, code, message string) *ServiceError {
	return &ServiceError{Status: status, Code: code, Message: message}
}

// InvalidRequest 构造一个 400 invalid_request 的 ServiceError。
//
// REST 之外的调用方（如 MCP 工具的参数互斥校验）需要与 REST 相同的错误形态与稳定 code，
// 但不能各自发明错误结构；导出这一个构造器，让它们复用同一条错误出口（DESIGN.md §7.3）。
func InvalidRequest(message string) *ServiceError {
	return newServiceError(http.StatusBadRequest, CodeInvalidRequest, message)
}

// asServiceError 把任意 error 归一成 ServiceError；未识别错误按 500 internal 处理。
func asServiceError(err error) *ServiceError {
	var se *ServiceError
	if errors.As(err, &se) {
		return se
	}
	return newServiceError(http.StatusInternalServerError, CodeInternal, err.Error())
}

// ---- 卡组 ----

// ListDecks 返回该用户可见的卡组（自有 ∪ 被 deck_grants 授权 ∪ 他人 public）。
//
// 口径与网页列表页（DeckStore.SummariesVisible）和复习队列的全库范围（DeckStore.VisibleIDs）
// 完全一致，谓词只有 store.visibleDeckIDsQuery 一份（DESIGN.md §5、§7.3）。REST 与内置 MCP
// 都调这里，任何一处改成 ListByOwner 都会让外部调用方看不到被共享的卡组（F5）。
func (a *API) ListDecks(ctx context.Context, userID uint64) ([]store.Deck, error) {
	decks, err := a.decks.ListVisible(ctx, userID)
	if err != nil {
		a.logger.Error("list decks failed", "user_id", userID, "error", err)
		return nil, newServiceError(http.StatusInternalServerError, CodeInternal, "failed to list decks")
	}
	return decks, nil
}

// CreateDeckInput 是建卡组的输入（DESIGN.md §7.3）。
// APIKeyID 仅用于审计条目，会话通道（网页登录）下为 nil。
type CreateDeckInput struct {
	Name        string
	Description string
	Visibility  string
	PresetID    uint64
	APIKeyID    *uint64
}

// CreateDeck 建一个空卡组（scope: write）；REST 与内置 MCP 共用这一份实现（DESIGN.md §7.4）。
//
// 行为与错误 code 与 REST handler 旧实现完全一致：name 去空白后必填，visibility 缺省
// private，preset_id 为 0 时使用（或创建）调用者的 Default 预设；store 的任何拒绝都映射成
// invalid_request（含非法 visibility），默认预设无法确保时映射成 internal_error。
func (a *API) CreateDeck(ctx context.Context, u *store.User, in CreateDeckInput) (*store.Deck, error) {
	name := strings.TrimSpace(in.Name)
	if name == "" {
		return nil, newServiceError(http.StatusBadRequest, CodeInvalidRequest, "")
	}
	visibility := strings.TrimSpace(in.Visibility)
	if visibility == "" {
		visibility = "private"
	}
	presetID := in.PresetID
	if presetID == 0 {
		id, err := a.ensureDefaultPreset(ctx, u.ID)
		if err != nil {
			a.logger.Error("ensure default preset failed", "user_id", u.ID, "error", err)
			return nil, newServiceError(http.StatusInternalServerError, CodeInternal, "")
		}
		presetID = id
	}
	d := store.Deck{
		OwnerUserID: u.ID,
		Name:        name,
		Description: in.Description,
		Visibility:  visibility,
		PresetID:    presetID,
		CreatedAt:   a.now(),
	}
	if err := a.decks.Create(ctx, &d); err != nil {
		return nil, newServiceError(http.StatusBadRequest, CodeInvalidRequest, err.Error())
	}
	a.audit(ctx, store.AuditEntry{
		UserID:     store.Ptr(u.ID),
		APIKeyID:   in.APIKeyID,
		Action:     "deck.create",
		TargetType: "deck",
		TargetID:   store.Ptr(d.ID),
		Detail:     map[string]any{"name": d.Name, "visibility": d.Visibility},
	})
	return &d, nil
}

// RequireDeckRole 校验用户在卡组上至少拥有 want 角色（M5-1）。
//
// 判定本体在 auth.DeckAccess（REST 与内置 MCP 共用同一实现）；这里把它翻译成带稳定
// code 的 ServiceError 并写一条 permission.denied 审计。无访问权 -> forbidden，
// 有角色但不够 -> insufficient_role，卡组不存在 -> not_found。
func (a *API) RequireDeckRole(ctx context.Context, userID, deckID uint64, want string) (*store.Deck, error) {
	deck, role, err := a.access.RequireRole(ctx, deckID, userID, want)
	if err == nil {
		return deck, nil
	}
	if errors.Is(err, auth.ErrDeckNotFound) {
		return nil, newServiceError(http.StatusNotFound, CodeNotFound, "deck not found")
	}
	code := CodeForbidden
	if role != "" {
		code = CodeInsufficientRole
	}
	a.audit(ctx, store.AuditEntry{
		UserID:     store.Ptr(userID),
		Action:     store.ActionPermissionDenied,
		TargetType: "deck",
		TargetID:   store.Ptr(deckID),
		Detail:     map[string]any{"required_role": want, "user_role": role, "code": code},
	})
	return nil, newServiceError(http.StatusForbidden, code, "insufficient role for this deck")
}

// RequireNoteRole 取 note 及其卡组并要求至少 want 角色；note 不存在返回 404。
func (a *API) RequireNoteRole(ctx context.Context, userID, noteID uint64, want string) (*store.Note, *store.Deck, error) {
	n, err := a.notes.ByID(ctx, noteID)
	if err != nil {
		return nil, nil, newServiceError(http.StatusNotFound, CodeNotFound, "note not found")
	}
	d, err := a.RequireDeckRole(ctx, userID, n.DeckID, want)
	if err != nil {
		return nil, nil, err
	}
	return n, d, nil
}

// countPlan 把一条规划计入 dry_run 的计数。
func countPlan(resp *ImportResponse, action string) {
	switch action {
	case "create":
		resp.Created++
	case "update":
		resp.Updated++
	case "skip":
		resp.Skipped++
	}
}

// findNoteByExternalRef 按 (deck_id, external_ref) 查 note，包含已软删除的行
// —— 唯一索引对软删除行同样生效，必须 Unscoped 查询，否则重复导入会撞唯一约束。
func (a *API) findNoteByExternalRef(ctx context.Context, deckID uint64, ref string) (*store.Note, error) {
	var n store.Note
	err := a.db.WithContext(ctx).Unscoped().
		Where("deck_id = ? AND external_ref = ?", deckID, ref).First(&n).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &n, nil
}

// ---- 卡片 ----

// ListNotes 返回卡组下的卡片列表（分页、标签过滤、关键词搜索）。
func (a *API) ListNotes(ctx context.Context, userID, deckID uint64, opts store.NoteListOptions) ([]store.Note, int64, error) {
	d, err := a.RequireDeckRole(ctx, userID, deckID, store.RoleReader)
	if err != nil {
		return nil, 0, err
	}
	if opts.Page < 1 {
		opts.Page = 1
	}
	if opts.PerPage <= 0 {
		opts.PerPage = store.DefaultNotePageSize
	}
	opts.DeckID = d.ID
	notes, total, err := a.notes.List(ctx, opts)
	if err != nil {
		a.logger.Error("list notes failed", "deck_id", d.ID, "error", err)
		return nil, 0, newServiceError(http.StatusInternalServerError, CodeInternal, "failed to list notes")
	}
	return notes, total, nil
}

// ImportNote 是批量导入的单个 note；字段名与 schema/note-import.schema.json 一致。
type ImportNote struct {
	Kind        string         `json:"kind"`
	Fields      map[string]any `json:"fields"`
	ExternalRef string         `json:"external_ref"`
	// NoteID 按主键寻址已有 note（M4-12）；与 ExternalRef 互斥。
	NoteID uint64   `json:"note_id"`
	Tags   []string `json:"tags"`
}

// ImportRequest 是批量新增/更新请求体；dry_run 与 on_conflict 是请求体字段。
type ImportRequest struct {
	DryRun     bool         `json:"dry_run"`
	OnConflict string       `json:"on_conflict"`
	Notes      []ImportNote `json:"notes"`
}

// ImportError 是逐条导入错误：index 指向请求体 notes 数组的下标。
type ImportError struct {
	Index  int    `json:"index"`
	Reason string `json:"reason"`
}

// ImportResponse 是批量导入的响应体（DESIGN.md §7.3）。
type ImportResponse struct {
	DryRun  bool          `json:"dry_run"`
	Created int           `json:"created"`
	Updated int           `json:"updated"`
	Skipped int           `json:"skipped"`
	Errors  []ImportError `json:"errors"`
}

// MaxImportNotes 是单次批量导入的上限（DESIGN.md §7.3：单次 ≤ 500）。
const MaxImportNotes = 500

// ImportBatchSize 是批量导入每个事务写入的行数（DESIGN.md §10.4：默认 200 条/事务）。
// 分批提交把每行一次 fsync 降到每 200 行一次，同时让\"失败可续\"成立：进程在导入中途
// 退出时，已提交的批次留在库里，重试同一请求会按 external_ref 幂等命中，不会重复建卡。
const ImportBatchSize = 200

// importPlan 是批量导入单行的处理计划：第一遍校验后得出该行是建、改还是跳过。
type importPlan struct {
	index    int
	action   string
	existing *store.Note
	note     store.Note
	fields   map[string]any
}

// ImportNotes 批量新增/更新卡片：按 (deck_id, external_ref) 幂等（DESIGN.md §7.3）。
//
// 采用两遍法：第一遍校验并规划每条的去向（create/update/skip），第二遍才写库。
// 第二遍按 ImportBatchSize 分批，每批一个事务；批内单行写失败用保存点回滚该行并
// 记录带下标的原因，合法行照常导入，因此\"一个含单行非法数据的批次会把合法行导入并
// 在报告里给出失败行的索引\"。dry_run 时停在第一遍，只返回计数；on_conflict=fail 时
// 任何冲突或校验错误都会整批拒绝。
func (a *API) ImportNotes(ctx context.Context, userID, deckID uint64, apiKeyID *uint64, req ImportRequest) (ImportResponse, error) {
	d, err := a.RequireDeckRole(ctx, userID, deckID, store.RoleEditor)
	if err != nil {
		return ImportResponse{}, err
	}
	if len(req.Notes) == 0 || len(req.Notes) > MaxImportNotes {
		return ImportResponse{}, newServiceError(http.StatusBadRequest, CodeInvalidRequest,
			fmt.Sprintf("notes must contain between 1 and %d items", MaxImportNotes))
	}
	onConflict := strings.TrimSpace(req.OnConflict)
	if onConflict == "" {
		onConflict = "update"
	}
	if onConflict != "skip" && onConflict != "update" && onConflict != "fail" {
		return ImportResponse{}, newServiceError(http.StatusBadRequest, CodeInvalidRequest, "on_conflict must be one of skip, update, fail")
	}

	plans := make([]importPlan, 0, len(req.Notes))
	resp := ImportResponse{DryRun: req.DryRun, Errors: []ImportError{}}
	hasFailure := false

	for i, item := range req.Notes {
		item.Kind = strings.TrimSpace(item.Kind)
		if item.Kind == "" {
			resp.Errors = append(resp.Errors, ImportError{i, "kind is required"})
			hasFailure = true
			continue
		}
		if len(item.Fields) == 0 {
			resp.Errors = append(resp.Errors, ImportError{i, "fields are required"})
			hasFailure = true
			continue
		}
		if err := cardtype.Validate(item.Kind, item.Fields); err != nil {
			resp.Errors = append(resp.Errors, ImportError{i, err.Error()})
			hasFailure = true
			continue
		}

		if item.NoteID != 0 && strings.TrimSpace(item.ExternalRef) != "" {
			// 两种寻址方式互斥：同时给出无法判定按哪个定位，直接判该行非法（M4-12）。
			resp.Errors = append(resp.Errors, ImportError{i, "note_id and external_ref are mutually exclusive"})
			hasFailure = true
			continue
		}

		ref := strings.TrimSpace(item.ExternalRef)
		byID := item.NoteID != 0
		var existing *store.Note
		if byID {
			// 按主键寻址：只认属于本卡组的、未软删的 note。取不到与跨卡组用同一句
			// 「note not found in deck」，不泄露另一个卡组是否存在该 id（M4-12）。
			found, err := a.notes.ByID(ctx, item.NoteID)
			if err != nil || found.DeckID != d.ID {
				resp.Errors = append(resp.Errors, ImportError{i, "note not found in deck"})
				hasFailure = true
				continue
			}
			existing = found
		} else if ref != "" {
			found, err := a.findNoteByExternalRef(ctx, d.ID, ref)
			if err != nil {
				a.logger.Error("lookup note by external_ref failed", "deck_id", d.ID, "error", err)
				return ImportResponse{}, newServiceError(http.StatusInternalServerError, CodeInternal, "failed to load existing notes")
			}
			existing = found
		}

		n := store.Note{
			DeckID:    d.ID,
			Kind:      item.Kind,
			TagsJSON:  tagsJSON(item.Tags),
			CreatedBy: store.Ptr(userID),
			Source:    store.Ptr("api"),
		}
		if ref != "" {
			n.ExternalRef = store.Ptr(ref)
		}

		action := "create"
		if existing != nil {
			if byID {
				// note_id 行按定义一定已存在，on_conflict 对它不适用：一律走更新，
				// 也不因它触发 on_conflict=fail 的整批拒绝（M4-12）。
				action = "update"
			} else {
				switch onConflict {
				case "skip":
					action = "skip"
				case "fail":
					resp.Errors = append(resp.Errors, ImportError{i, "a note with this external_ref already exists"})
					hasFailure = true
					continue
				default:
					action = "update"
				}
			}
		}
		plans = append(plans, importPlan{index: i, action: action, existing: existing, note: n, fields: item.Fields})
	}

	if onConflict == "fail" && hasFailure {
		return ImportResponse{}, newServiceError(http.StatusConflict, CodeConflict, "import aborted: one or more notes are invalid or conflict")
	}

	if req.DryRun {
		for _, p := range plans {
			countPlan(&resp, p.action)
		}
		return resp, nil
	}

	// 第二遍：按批写入。每批一个事务；批内单行失败回滚到行保存点并记录下标，
	// 同批其余合法行照常导入。批与批之间独立提交，因此失败可续。
	for start := 0; start < len(plans); start += ImportBatchSize {
		end := start + ImportBatchSize
		if end > len(plans) {
			end = len(plans)
		}
		batch := plans[start:end]
		batchErr := a.writeImportBatch(ctx, batch, &resp)
		if batchErr != nil {
			// 基础设施级失败（保存点/提交失败）：记录该批首行的下标并继续下一批，
			// 已提交的批次不回滚；重试时 external_ref 幂等保证不重复建卡。
			a.logger.Error("import batch failed", "deck_id", d.ID, "start_index", batch[0].index, "error", batchErr)
			resp.Errors = append(resp.Errors, ImportError{batch[0].index, batchErr.Error()})
		}
	}

	a.audit(ctx, store.AuditEntry{
		UserID:     store.Ptr(userID),
		APIKeyID:   apiKeyID,
		Action:     "note.import",
		TargetType: "deck",
		TargetID:   store.Ptr(d.ID),
		Detail: map[string]any{
			"created": resp.Created, "updated": resp.Updated,
			"skipped": resp.Skipped, "errors": len(resp.Errors), "dry_run": req.DryRun,
		},
	})
	return resp, nil
}

// writeImportBatch 在一个事务里写入一批导入计划；返回非 nil 表示批级失败（已回滚）。
//
// 逐行用 SAVEPOINT 包裹：PostgreSQL 在语句报错后会中止整个事务，必须 ROLLBACK TO SAVEPOINT
// 才能继续处理同批的其它行；SQLite 同样支持该语法。单行错误写进 resp.Errors（带请求体下标）。
func (a *API) writeImportBatch(ctx context.Context, batch []importPlan, resp *ImportResponse) error {
	return a.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		for _, p := range batch {
			if p.action == "skip" {
				resp.Skipped++
				continue
			}
			savepoint := fmt.Sprintf("import_row_%d", p.index)
			if err := tx.SavePoint(savepoint).Error; err != nil {
				return fmt.Errorf("open savepoint for row %d: %w", p.index, err)
			}
			var rowErr error
			switch p.action {
			case "create":
				n := p.note
				_, rowErr = a.notes.CreateInTx(ctx, tx, &n, p.fields)
			default: // update
				if p.existing.DeletedAt.Valid {
					rowErr = a.notes.RestoreInTx(ctx, tx, p.existing.ID)
				}
				if rowErr == nil {
					n := p.note
					n.ID = p.existing.ID
					_, rowErr = a.notes.UpdateInTx(ctx, tx, &n, p.fields)
				}
			}
			if rowErr != nil {
				if rbErr := tx.RollbackTo(savepoint).Error; rbErr != nil {
					return fmt.Errorf("rollback savepoint for row %d: %w", p.index, rbErr)
				}
				resp.Errors = append(resp.Errors, ImportError{p.index, rowErr.Error()})
				continue
			}
			if p.action == "create" {
				resp.Created++
			} else {
				resp.Updated++
			}
		}
		return nil
	})
}

// UpdateNoteInput 是单卡更新输入；Tags 用指针区分“未提供”与“清空”。
type UpdateNoteInput struct {
	Kind   string
	Fields map[string]any
	Tags   *[]string
}

// UpdateNote 更新单卡内容；已有 card 的 id 与用户进度保持不变（NoteStore.Update 的保证）。
func (a *API) UpdateNote(ctx context.Context, userID, noteID uint64, apiKeyID *uint64, in UpdateNoteInput) (*store.Note, error) {
	existing, _, err := a.RequireNoteRole(ctx, userID, noteID, store.RoleEditor)
	if err != nil {
		return nil, err
	}
	if len(in.Fields) == 0 {
		return nil, newServiceError(http.StatusBadRequest, CodeInvalidRequest, "fields are required")
	}
	kind := strings.TrimSpace(in.Kind)
	if kind == "" {
		kind = existing.Kind
	}
	// TODO(M4-9): error message localisation via Accept-Language.
	if err := cardtype.Validate(kind, in.Fields); err != nil {
		return nil, newServiceError(http.StatusBadRequest, CodeInvalidRequest, err.Error())
	}
	n := store.Note{ID: existing.ID, Kind: kind}
	if in.Tags != nil {
		n.TagsJSON = tagsJSON(*in.Tags)
	} else {
		n.TagsJSON = existing.TagsJSON
	}
	if _, err := a.notes.Update(ctx, &n, in.Fields); err != nil {
		a.logger.Error("update note failed", "note_id", existing.ID, "error", err)
		return nil, newServiceError(http.StatusBadRequest, CodeInvalidRequest, err.Error())
	}
	a.audit(ctx, store.AuditEntry{
		UserID:     store.Ptr(userID),
		APIKeyID:   apiKeyID,
		Action:     store.ActionNoteUpdate,
		TargetType: "note",
		TargetID:   store.Ptr(existing.ID),
	})
	updated, err := a.notes.ByID(ctx, existing.ID)
	if err != nil {
		return nil, newServiceError(http.StatusInternalServerError, CodeInternal, "failed to reload note")
	}
	return updated, nil
}

// DeleteNote 软删除单卡（进度保留，误删可恢复）；返回被删除的 note id。
func (a *API) DeleteNote(ctx context.Context, userID, noteID uint64, apiKeyID *uint64) (uint64, error) {
	existing, _, err := a.RequireNoteRole(ctx, userID, noteID, store.RoleEditor)
	if err != nil {
		return 0, err
	}
	if err := a.notes.Delete(ctx, existing.ID); err != nil {
		a.logger.Error("delete note failed", "note_id", existing.ID, "error", err)
		return 0, newServiceError(http.StatusInternalServerError, CodeInternal, "failed to delete note")
	}
	a.audit(ctx, store.AuditEntry{
		UserID:     store.Ptr(userID),
		APIKeyID:   apiKeyID,
		Action:     store.ActionNoteDelete,
		TargetType: "note",
		TargetID:   store.Ptr(existing.ID),
	})
	return existing.ID, nil
}

// ---- 批量卡片动作（M4-12）----

// 批量动作的稳定英文取值（REST 请求体 action 字段、审计 detail.action 共用）。
const (
	bulkActionDelete     = "delete"
	bulkActionAddTags    = "add_tags"
	bulkActionRemoveTags = "remove_tags"
	bulkActionSetTags    = "set_tags"
)

// bulkMaxTags 是单个标签动作允许的标签数上限（去重后）。
const bulkMaxTags = 20

// BulkNotesInput 是批量卡片动作的输入（REST 与内置 MCP 共用，DESIGN.md §2.4 一个模型服务业务与 JSON）。
type BulkNotesInput struct {
	Action  string   `json:"action"`
	NoteIDs []uint64 `json:"note_ids"`
	Tags    []string `json:"tags"`
	DryRun  bool     `json:"dry_run"`
}

// BulkNotesSkipped 是被逐行拒绝的 note：code 取值 not_found / insufficient_role。
type BulkNotesSkipped struct {
	NoteID uint64 `json:"note_id"`
	Code   string `json:"code"`
}

// BulkNotesResponse 是批量动作的响应体（DESIGN.md §7.3）。Affected 只计真正改动的行，
// 因此重复提交同一请求第二次返回 affected=0。
type BulkNotesResponse struct {
	DryRun   bool               `json:"dry_run"`
	Affected int64              `json:"affected"`
	Skipped  []BulkNotesSkipped `json:"skipped"`
}

// BulkNotes 对一组 note 执行 delete / add_tags / remove_tags / set_tags（M4-12）。
//
// 请求级校验（未知 action、note_ids 越界、tag 动作缺 tags、delete 带 tags）返回 400
// invalid_request 且一行都不写。通过校验后逐行判权：缺失或已软删的 id 记为 not_found，
// 无权限或角色不够的 id 记为 insufficient_role，其余行照常处理 —— 单行被拒不回滚整批。
// 审计只在非 dry_run 时整批一行；dry_run 只计数不写库、不写审计。
func (a *API) BulkNotes(ctx context.Context, userID uint64, apiKeyID *uint64, in BulkNotesInput) (BulkNotesResponse, error) {
	action := strings.TrimSpace(in.Action)
	switch action {
	case bulkActionDelete, bulkActionAddTags, bulkActionRemoveTags, bulkActionSetTags:
	default:
		return BulkNotesResponse{}, newServiceError(http.StatusBadRequest, CodeInvalidRequest,
			"action must be one of delete, add_tags, remove_tags, set_tags")
	}

	ids := uniqueIDs(in.NoteIDs)
	if len(ids) == 0 || len(ids) > MaxImportNotes {
		return BulkNotesResponse{}, newServiceError(http.StatusBadRequest, CodeInvalidRequest,
			fmt.Sprintf("note_ids must contain between 1 and %d entries", MaxImportNotes))
	}

	tags := store.NormalizeTags(in.Tags)
	if action == bulkActionDelete {
		if len(in.Tags) > 0 {
			return BulkNotesResponse{}, newServiceError(http.StatusBadRequest, CodeInvalidRequest,
				"tags are not allowed for action delete")
		}
	} else if len(tags) == 0 || len(tags) > bulkMaxTags {
		return BulkNotesResponse{}, newServiceError(http.StatusBadRequest, CodeInvalidRequest,
			fmt.Sprintf("tags must contain between 1 and %d entries", bulkMaxTags))
	}

	resp := BulkNotesResponse{DryRun: in.DryRun, Skipped: []BulkNotesSkipped{}}
	permitted := make([]uint64, 0, len(ids))
	for _, id := range ids {
		if code := a.bulkRowCode(ctx, userID, id, !in.DryRun); code != "" {
			// 单行判权失败不使整批失败：not_found 与 insufficient_role 各记一条 skipped，
			// 其余 id 继续处理。
			resp.Skipped = append(resp.Skipped, BulkNotesSkipped{NoteID: id, Code: code})
			continue
		}
		permitted = append(permitted, id)
	}

	var affected int64
	var err error
	switch action {
	case bulkActionDelete:
		affected, err = a.notes.DeleteMany(ctx, permitted, in.DryRun)
	case bulkActionAddTags:
		affected, err = a.notes.AddTags(ctx, permitted, tags, in.DryRun)
	case bulkActionRemoveTags:
		affected, err = a.notes.RemoveTags(ctx, permitted, tags, in.DryRun)
	case bulkActionSetTags:
		affected, err = a.notes.SetTags(ctx, permitted, tags, in.DryRun)
	}
	if err != nil {
		a.logger.Error("bulk note action failed", "action", action, "error", err)
		return BulkNotesResponse{}, newServiceError(http.StatusInternalServerError, CodeInternal, "failed to apply bulk note action")
	}
	resp.Affected = affected

	if !in.DryRun {
		// 与网页批量表单对齐：整批一行审计，detail 里带动作、请求的 id 列表与变更行数。
		a.audit(ctx, store.AuditEntry{
			UserID:     store.Ptr(userID),
			APIKeyID:   apiKeyID,
			Action:     bulkAuditAction(action),
			TargetType: "notes",
			Detail:     map[string]any{"action": action, "ids": ids, "affected": affected},
		})
	}
	return resp, nil
}

// bulkRowCode 判定调用者能否编辑该 note：可编辑返回空串，否则返回该行 skipped 里的稳定 code
// （与错误 code 同一套取值：not_found / insufficient_role）。
//
// audit 为 true 时走共享的 RequireNoteRole —— 它会在角色不足时写一条 permission.denied 审计，
// 与 REST 其它写操作的取证口径一致（谁在什么时候试图改什么被挡下）。dry_run 传 false，逐行
// 静默判定：一次 dry run 若有 500 个 id 都无权限，不该抖出 500 条审计行（§7.3：dry_run 不写
// 任何行、不写审计行、零副作用）。两条路径对同一情形必须给出同一个 code。
func (a *API) bulkRowCode(ctx context.Context, userID, noteID uint64, audit bool) string {
	var err error
	if audit {
		_, _, err = a.RequireNoteRole(ctx, userID, noteID, store.RoleEditor)
	} else {
		var n *store.Note
		if n, err = a.notes.ByID(ctx, noteID); err == nil {
			// 与 RequireNoteRole 同一条判定链，只是不写 permission.denied（见上）。
			_, _, err = a.access.RequireRole(ctx, n.DeckID, userID, store.RoleEditor)
		}
	}
	if err == nil {
		return ""
	}
	if errors.Is(err, gorm.ErrRecordNotFound) || errors.Is(err, auth.ErrDeckNotFound) ||
		asServiceError(err).Code == CodeNotFound {
		// 不存在、已软删（ByID 不返回软删行），或卡组已消失：一律 not_found。
		return CodeNotFound
	}
	return CodeInsufficientRole
}

// bulkAuditAction 把批量动作映射到稳定的审计动作名（constants 定义在 store/audit.go）。
func bulkAuditAction(action string) string {
	switch action {
	case bulkActionDelete:
		return store.ActionNoteDelete
	case bulkActionAddTags:
		return store.ActionNoteTagAdd
	case bulkActionRemoveTags:
		return store.ActionNoteTagRemove
	default:
		return store.ActionNoteTagSet
	}
}

// uniqueIDs 按首次出现去重并保持请求顺序；批量动作的响应与审计都依赖这个顺序。
func uniqueIDs(ids []uint64) []uint64 {
	seen := make(map[uint64]struct{}, len(ids))
	out := make([]uint64, 0, len(ids))
	for _, id := range ids {
		if _, ok := seen[id]; ok {
			continue
		}
		seen[id] = struct{}{}
		out = append(out, id)
	}
	return out
}

// ---- 统计与导出 ----

// StatsSummary 是 /stats/summary 与 get_stats 工具的共同响应形态。
type StatsSummary struct {
	Decks        int     `json:"decks"`
	Due          int64   `json:"due"`
	ReviewsToday int64   `json:"reviews_today"`
	ReviewsTotal int64   `json:"reviews_total"`
	Retention    float64 `json:"retention"`
	Notes        int64   `json:"notes"`
	Cards        int64   `json:"cards"`
}

// Stats 汇总当前用户的到期量 / 复习量 / 留存概要；所有数字都由 reviews + card_states 聚合。
//
// 口径刻意不对称（DESIGN.md §9 的 2026-10-06 决定）：decks/due/notes/cards 只算当前可见
// 卡组（与 list_decks、网页列表页、复习队列同一个 store 谓词），卡组集合为空时它们为 0；
// reviews_today / reviews_total / retention 按 user_id 保留全史——复习是本人的记录，
// 撤销授权不追溯。因此即便一个可见卡组都没有，也必须继续聚合 reviews，不能提前返回把
// 历史数字静默清零（F6）。
func (a *API) Stats(ctx context.Context, u *store.User) (StatsSummary, error) {
	now := a.now()

	deckIDs, err := a.decks.VisibleIDs(ctx, u.ID)
	if err != nil {
		a.logger.Error("load visible decks for stats failed", "user_id", u.ID, "error", err)
		return StatsSummary{}, newServiceError(http.StatusInternalServerError, CodeInternal, "failed to load statistics")
	}
	resp := StatsSummary{Decks: len(deckIDs)}

	var due int64
	if err := a.db.WithContext(ctx).Model(&store.CardState{}).
		Joins("JOIN cards ON cards.id = card_states.card_id AND cards.deleted_at IS NULL").
		Joins("JOIN notes ON notes.id = cards.note_id AND notes.deleted_at IS NULL").
		Where("notes.deck_id IN ?", deckIDs).
		Where("card_states.user_id = ? AND card_states.due_at IS NOT NULL AND card_states.due_at <= ?", u.ID, now).
		Count(&due).Error; err != nil {
		a.logger.Error("count due cards failed", "user_id", u.ID, "error", err)
		return StatsSummary{}, newServiceError(http.StatusInternalServerError, CodeInternal, "failed to load statistics")
	}

	day := schedule.ReviewDay(now, userLocation(u.Timezone), u.DayCutoffHour)
	var reviewsToday int64
	if err := a.db.WithContext(ctx).Model(&store.Review{}).
		Where("user_id = ? AND review_day = ?", u.ID, day).Count(&reviewsToday).Error; err != nil {
		a.logger.Error("count reviews today failed", "user_id", u.ID, "error", err)
		return StatsSummary{}, newServiceError(http.StatusInternalServerError, CodeInternal, "failed to load statistics")
	}
	var reviewsTotal int64
	if err := a.db.WithContext(ctx).Model(&store.Review{}).
		Where("user_id = ?", u.ID).Count(&reviewsTotal).Error; err != nil {
		a.logger.Error("count reviews failed", "user_id", u.ID, "error", err)
		return StatsSummary{}, newServiceError(http.StatusInternalServerError, CodeInternal, "failed to load statistics")
	}
	// 留存近似口径：非 Again 的比例（更精细的分桶留给 M7）。
	nonAgain, err := countNonAgain(ctx, a, u.ID)
	if err != nil {
		a.logger.Error("count retention failed", "user_id", u.ID, "error", err)
		return StatsSummary{}, newServiceError(http.StatusInternalServerError, CodeInternal, "failed to load statistics")
	}

	var notes, cards int64
	if err := a.db.WithContext(ctx).Model(&store.Note{}).
		Where("deck_id IN ?", deckIDs).Count(&notes).Error; err != nil {
		a.logger.Error("count notes failed", "user_id", u.ID, "error", err)
		return StatsSummary{}, newServiceError(http.StatusInternalServerError, CodeInternal, "failed to load statistics")
	}
	if err := a.db.WithContext(ctx).Model(&store.Card{}).
		Joins("JOIN notes ON notes.id = cards.note_id AND notes.deleted_at IS NULL").
		Where("notes.deck_id IN ?", deckIDs).Where("cards.deleted_at IS NULL").
		Count(&cards).Error; err != nil {
		a.logger.Error("count cards failed", "user_id", u.ID, "error", err)
		return StatsSummary{}, newServiceError(http.StatusInternalServerError, CodeInternal, "failed to load statistics")
	}

	resp.Due = due
	resp.ReviewsToday = reviewsToday
	resp.ReviewsTotal = reviewsTotal
	resp.Notes = notes
	resp.Cards = cards
	if reviewsTotal > 0 {
		resp.Retention = float64(nonAgain) / float64(reviewsTotal)
	}
	return resp, nil
}

// ExportRow 是卡片级导出的扁平行（DESIGN.md §7.5：给外部工具用的粒度）。
type ExportRow struct {
	CardID      uint64         `json:"card_id"`
	NoteID      uint64         `json:"note_id"`
	DeckID      uint64         `json:"deck_id"`
	Kind        string         `json:"kind"`
	Template    string         `json:"template"`
	Fields      map[string]any `json:"fields"`
	Tags        []string       `json:"tags"`
	ExternalRef *string        `json:"external_ref,omitempty"`
	State       *string        `json:"state,omitempty"`
	DueAt       *time.Time     `json:"due_at,omitempty"`
	Reps        *int           `json:"reps,omitempty"`
	Lapses      *int           `json:"lapses,omitempty"`
}

// ExportDeckIDs 解析导出目标卡组集合：deckID=0 时导出调用者可见的卡组，否则仅该卡组。
//
// 「全部导出」与 list_decks 同口径（自有 ∪ 被授权 ∪ 他人 public，DESIGN.md §7.5 的
// 2026-10-06 决定），不得静默扩大成库内全部卡组；可见性谓词只有 store 一份（F5）。
func (a *API) ExportDeckIDs(ctx context.Context, userID, deckID uint64) ([]uint64, error) {
	if deckID != 0 {
		if _, err := a.RequireDeckRole(ctx, userID, deckID, store.RoleReader); err != nil {
			return nil, err
		}
		return []uint64{deckID}, nil
	}
	ids, err := a.decks.VisibleIDs(ctx, userID)
	if err != nil {
		return nil, newServiceError(http.StatusInternalServerError, CodeInternal, "failed to load decks")
	}
	return ids, nil
}

// ---- 复习 ----

// DueCard 是到期卡的对外形态（含字段原文）。
type DueCard struct {
	CardID         uint64         `json:"card_id"`
	NoteID         uint64         `json:"note_id"`
	DeckID         uint64         `json:"deck_id"`
	State          string         `json:"state"`
	DueAt          time.Time      `json:"due_at"`
	Retrievability float64        `json:"retrievability"`
	Kind           string         `json:"kind"`
	Fields         map[string]any `json:"fields"`
	Tags           []string       `json:"tags"`
	Template       string         `json:"template,omitempty"`
}

// DueCards 返回到期卡（含字段原文）；deckIDs 为空表示全部卡组；limit 取 [1,500]。
//
// 每个卡组 id 都要求至少 reader 角色：任一个不可读或不存在即整次调用失败（不静默过滤）。
// 只有恰好指定一个卡组时才用该卡组的预设构造调度器；多卡组与全库用默认预设（DESIGN.md §3.3）。
func (a *API) DueCards(ctx context.Context, u *store.User, deckIDs []uint64, limit int) ([]DueCard, error) {
	if limit < 1 {
		limit = 1
	}
	if limit > 500 {
		limit = 500
	}
	ids := dedupeDeckIDs(deckIDs)
	var deck *store.Deck
	for _, id := range ids {
		d, err := a.RequireDeckRole(ctx, u.ID, id, store.RoleReader)
		if err != nil {
			return nil, err
		}
		if len(ids) == 1 {
			deck = d
		}
	}

	// 队列构建需要调度器（只为复习卡算 retrievability）；多卡组/无卡组时用默认预设。
	var sched *schedule.Scheduler
	if deck != nil {
		s, err := a.schedulerForDeck(ctx, deck)
		if err != nil {
			return nil, newServiceError(http.StatusInternalServerError, CodeInternal, "failed to load deck scheduler")
		}
		sched = s
	} else {
		presetID, err := a.ensureDefaultPreset(ctx, u.ID)
		if err != nil {
			return nil, newServiceError(http.StatusInternalServerError, CodeInternal, "failed to load default preset")
		}
		preset, err := a.presets.ByID(ctx, presetID)
		if err != nil {
			return nil, newServiceError(http.StatusInternalServerError, CodeInternal, "failed to load default preset")
		}
		s, err := schedule.NewScheduler(preset)
		if err != nil {
			return nil, newServiceError(http.StatusInternalServerError, CodeInternal, "failed to build scheduler")
		}
		sched = s
	}

	builder := schedule.NewQueueBuilder(a.db, a.decks, sched)
	opts := schedule.QueueOptions{
		Now:           a.now(),
		Timezone:      u.Timezone,
		DayCutoffHour: u.DayCutoffHour,
		ReviewOrder:   schedule.OrderByDueAt,
		NewOrder:      schedule.NewOrderRandom,
	}
	// 单卡组走 DeckID（保留卡组上限口径）；多卡组走 DeckIDs 集合。
	switch len(ids) {
	case 0:
	case 1:
		opts.DeckID = ids[0]
	default:
		opts.DeckIDs = ids
	}
	items, err := builder.Build(ctx, u.ID, opts)
	if err != nil {
		a.logger.Error("build due queue failed", "user_id", u.ID, "error", err)
		return nil, newServiceError(http.StatusInternalServerError, CodeInternal, "failed to build review queue")
	}
	if len(items) > limit {
		items = items[:limit]
	}

	out := make([]DueCard, 0, len(items))
	for _, it := range items {
		note, err := a.notes.ByID(ctx, it.NoteID)
		if err != nil {
			continue
		}
		entry := DueCard{
			CardID:         it.CardID,
			NoteID:         it.NoteID,
			DeckID:         it.DeckID,
			State:          it.State.String(),
			DueAt:          it.DueAt,
			Retrievability: it.Retrievability,
			Kind:           note.Kind,
			Fields:         fieldsOrEmpty(note),
			Tags:           tagsOrEmpty(note),
		}
		if card, err := a.cards.ByID(ctx, it.CardID); err == nil {
			entry.Template = card.Template
		}
		out = append(out, entry)
	}
	return out, nil
}

// dedupeDeckIDs 去掉重复的卡组 id，保持首次出现的顺序；0 不是合法卡组 id，一并丢弃。
func dedupeDeckIDs(ids []uint64) []uint64 {
	out := make([]uint64, 0, len(ids))
	seen := make(map[uint64]bool, len(ids))
	for _, id := range ids {
		if id == 0 || seen[id] {
			continue
		}
		seen[id] = true
		out = append(out, id)
	}
	return out
}

// SubmitReviewInput 是评分提交输入（DESIGN.md §3.4）。
type SubmitReviewInput struct {
	CardID          uint64
	Rating          int
	ExpectedVersion int
	ElapsedMS       *int
	GradeSource     string
}

// SubmitReviewResult 是评分提交的响应形态。
type SubmitReviewResult struct {
	CardID    uint64     `json:"card_id"`
	ReviewID  uint64     `json:"review_id"`
	State     string     `json:"state"`
	DueAt     *time.Time `json:"due_at"`
	Version   int        `json:"version"`
	Stability *float64   `json:"stability"`
}

// SubmitReview 提交一次评分；乐观锁不匹配返回 409 version_conflict。
func (a *API) SubmitReview(ctx context.Context, u *store.User, apiKeyID *uint64, in SubmitReviewInput) (SubmitReviewResult, error) {
	if in.CardID == 0 {
		return SubmitReviewResult{}, newServiceError(http.StatusBadRequest, CodeInvalidRequest, "card_id is required")
	}
	if !schedule.Rating(in.Rating).Valid() {
		return SubmitReviewResult{}, newServiceError(http.StatusBadRequest, CodeInvalidRequest, "rating must be between 1 and 4")
	}
	card, err := a.cards.ByID(ctx, in.CardID)
	if err != nil {
		return SubmitReviewResult{}, newServiceError(http.StatusNotFound, CodeNotFound, "card not found")
	}
	note, err := a.notes.ByID(ctx, card.NoteID)
	if err != nil {
		return SubmitReviewResult{}, newServiceError(http.StatusNotFound, CodeNotFound, "note not found")
	}
	deck, err := a.RequireDeckRole(ctx, u.ID, note.DeckID, store.RoleReader)
	if err != nil {
		return SubmitReviewResult{}, err
	}
	sched, err := a.schedulerForDeck(ctx, deck)
	if err != nil {
		return SubmitReviewResult{}, newServiceError(http.StatusInternalServerError, CodeInternal, "failed to load deck scheduler")
	}

	var result schedule.SubmitResult
	err = a.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var inner error
		result, inner = schedule.Submit(ctx, tx, schedule.SubmitInput{
			CardID:          in.CardID,
			UserID:          u.ID,
			Rating:          schedule.Rating(in.Rating),
			ExpectedVersion: in.ExpectedVersion,
			ElapsedMS:       in.ElapsedMS,
			GradeSource:     in.GradeSource,
			Scheduler:       sched,
			Now:             a.now(),
			Location:        userLocation(u.Timezone),
			Timezone:        u.Timezone,
			DayCutoffHour:   u.DayCutoffHour,
		})
		return inner
	})
	if err != nil {
		if errors.Is(err, schedule.ErrVersionConflict) {
			return SubmitReviewResult{}, newServiceError(http.StatusConflict, CodeVersionConflict, "card state version conflict")
		}
		a.logger.Error("submit review failed", "card_id", in.CardID, "user_id", u.ID, "error", err)
		return SubmitReviewResult{}, newServiceError(http.StatusInternalServerError, CodeInternal, "failed to submit review")
	}
	a.audit(ctx, store.AuditEntry{
		UserID:     store.Ptr(u.ID),
		APIKeyID:   apiKeyID,
		Action:     "review.submit",
		TargetType: "card",
		TargetID:   store.Ptr(in.CardID),
		Detail:     map[string]any{"rating": in.Rating, "review_id": result.ReviewID},
	})
	return SubmitReviewResult{
		CardID:    in.CardID,
		ReviewID:  result.ReviewID,
		State:     result.State.State,
		DueAt:     result.State.DueAt,
		Version:   result.State.Version,
		Stability: result.State.Stability,
	}, nil
}
