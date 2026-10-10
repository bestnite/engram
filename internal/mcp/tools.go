package mcp

import (
	"context"
	"encoding/json"
	"strings"

	"git.nite07.com/nite/engram/internal/api"
	"git.nite07.com/nite/engram/internal/store"
)

// 本文件定义九个 MCP 工具的入参类型与处理器；每个处理器只做参数整形，
// 随后调用与同名 REST 端点完全相同的 internal/api service 方法。
//
// 所有指向自增主键的入参都是对外 id（UUIDv7 字符串）：处理器先经 api 的
// ByPublicID 解析成数字主键，再进入 service 层；数字主键既不接受也不返回。

// ---- 入参 ----

// listDecksIn 无参数。
type listDecksIn struct{}

// createDeckIn 是 create_deck 的入参；preset_id 为空时使用（或创建）调用者的 Default 预设。
type createDeckIn struct {
	Name        string `json:"name" jsonschema:"the deck name (required)"`
	Description string `json:"description,omitempty" jsonschema:"optional deck description"`
	PresetID    string `json:"preset_id,omitempty" jsonschema:"scheduling preset public id; empty uses the caller's Default preset"`
}

// updateDeckIn 是 update_deck 的入参；两个字段都可省略，省略即保持原值（PATCH 语义）。
type updateDeckIn struct {
	DeckID      string  `json:"deck_id" jsonschema:"the public id of the deck to update"`
	Name        *string `json:"name,omitempty" jsonschema:"new deck name; omit to keep the current name"`
	Description *string `json:"description,omitempty" jsonschema:"new deck description; omit to keep it, an empty string clears it"`
}

// searchNotesIn 是 search_notes 的入参；deck_id 必填。
type searchNotesIn struct {
	DeckID  string `json:"deck_id" jsonschema:"the public id of the deck to search in"`
	Query   string `json:"q,omitempty" jsonschema:"keyword matched against note fields"`
	Tag     string `json:"tag,omitempty" jsonschema:"exact tag filter"`
	Kind    string `json:"kind,omitempty" jsonschema:"card type filter (e.g. basic, cloze)"`
	Status  string `json:"status,omitempty" jsonschema:"note status filter"`
	Page    int    `json:"page,omitempty" jsonschema:"page number, 1-based"`
	PerPage int    `json:"per_page,omitempty" jsonschema:"items per page"`
}

// getNoteIn 是 get_note 的入参。
type getNoteIn struct {
	NoteID string `json:"note_id" jsonschema:"the public id of the note to read"`
}

// getStatsIn 无参数。
type getStatsIn struct{}

// listCardTypesIn 是 list_card_types 的入参：无参数。
type listCardTypesIn struct{}

// exportDeckIn 是 export_deck 的入参；它导出一个**卡组包**。
type exportDeckIn struct {
	DeckID          string `json:"deck_id" jsonschema:"the public id of the deck to export as a package"`
	IncludeProgress bool   `json:"include_progress,omitempty" jsonschema:"include the caller's own review progress"`
	// IncludeMedia 缺省为 true（媒体默认内联）。
	IncludeMedia   *bool `json:"include_media,omitempty" jsonschema:"inline media bytes; default true"`
	IncludeReviews bool  `json:"include_reviews,omitempty" jsonschema:"include review logs (requires include_progress)"`
	// IncludeWeights 缺省 false：FSRS 权重是导出者个人的记忆曲线，不随卡组内容默认带出。
	IncludeWeights bool `json:"include_weights,omitempty" jsonschema:"include the caller's FSRS weights in preset.json; default false"`
}

// importNoteIn 是单个 note 的入参，字段名与 schema/note-import.schema.json 一致。
type importNoteIn struct {
	Kind        string         `json:"kind" jsonschema:"card type (e.g. basic, cloze); list_card_types returns every kind"`
	Fields      map[string]any `json:"fields" jsonschema:"field values for the card type; list_card_types returns each kind's fields"`
	ExternalRef string         `json:"external_ref,omitempty" jsonschema:"caller-defined idempotency key, unique per deck"`
	// NoteID 按对外 id 寻址已有 note 就地改写；与 external_ref 互斥。
	NoteID string   `json:"note_id,omitempty" jsonschema:"address an existing note by public id to rewrite in place; mutually exclusive with external_ref"`
	Tags   []string `json:"tags,omitempty" jsonschema:"note tags"`
}

// bulkNotesIn 是 create_notes 的入参（批量建卡路径）。
type bulkNotesIn struct {
	DeckID     string         `json:"deck_id" jsonschema:"public id of the target deck"`
	Notes      []importNoteIn `json:"notes" jsonschema:"notes to create or update (1..500)"`
	DryRun     bool           `json:"dry_run,omitempty" jsonschema:"validate and count without writing"`
	OnConflict string         `json:"on_conflict,omitempty" jsonschema:"conflict policy: skip, update (default) or fail"`
}

// bulkActionIn 是 bulk_notes 的入参，与 REST 的 POST /notes/bulk 请求体同形
// （MCP 只做参数整形，语义与校验都在 service 层）。
type bulkActionIn struct {
	Action  string   `json:"action" jsonschema:"bulk action: delete, add_tags, remove_tags or set_tags"`
	NoteIDs []string `json:"note_ids" jsonschema:"public ids of the notes to act on, deduplicated to 1..500 entries"`
	Tags    []string `json:"tags,omitempty" jsonschema:"tags for the tag actions, 1..20 entries; not allowed for delete"`
	DryRun  bool     `json:"dry_run,omitempty" jsonschema:"count without writing or auditing"`
}

// importDeckIn 是 import_deck 的入参：接受一个卡组包，来源二选一。
type importDeckIn struct {
	// Package 是包本体：export_deck 输出的 JSON 文档，或 base64 编码的 .edeck zip。与 URL 互斥。
	Package any `json:"package,omitempty" jsonschema:"the deck package: export_deck's JSON document, or a base64-encoded .edeck archive; mutually exclusive with url"`
	// URL 是公开 HTTPS 直链（.edeck/.zip）；由服务端下载并校验，与 Package 互斥。
	URL string `json:"url,omitempty" jsonschema:"public HTTPS direct link to a .edeck/.zip package; mutually exclusive with package"`
	// Target 取值 new_deck（默认）、into_deck:<public id>、replace_deck:<public id>。
	Target     string `json:"target,omitempty" jsonschema:"import target: new_deck (default), into_deck:<public id> or replace_deck:<public id>"`
	DryRun     bool   `json:"dry_run,omitempty" jsonschema:"validate and count without writing"`
	OnConflict string `json:"on_conflict,omitempty" jsonschema:"conflict policy: skip, update (default) or fail"`
	// SkipMissingMedia 缺失媒体时只计数并继续；默认 false（缺媒体即失败）。
	SkipMissingMedia bool `json:"skip_missing_media,omitempty" jsonschema:"skip missing media instead of failing"`
	// AllowOthersProgress 仅管理员可置位：允许导入包内他人的进度。
	AllowOthersProgress bool `json:"allow_others_progress,omitempty" jsonschema:"admin only: import progress that belongs to another user"`
	// ApplyWeights 缺省 false：只在 new_deck 目标下把包内权重写进新建的预设。
	ApplyWeights bool `json:"apply_weights,omitempty" jsonschema:"use the package's FSRS weights for the new deck's preset (new_deck only); default false"`
}

// createImportUploadIn 是 create_import_upload 的入参：导入选项在签发时固定进票据。
type createImportUploadIn struct {
	// Target 取值 new_deck（默认）、into_deck:<public id>、replace_deck:<public id>。
	Target     string `json:"target,omitempty" jsonschema:"import target: new_deck (default), into_deck:<public id> or replace_deck:<public id>"`
	DryRun     bool   `json:"dry_run,omitempty" jsonschema:"validate and count without writing"`
	OnConflict string `json:"on_conflict,omitempty" jsonschema:"conflict policy: skip, update (default) or fail"`
	// SkipMissingMedia 缺失媒体时只计数并继续；默认 false（缺媒体即失败）。
	SkipMissingMedia bool `json:"skip_missing_media,omitempty" jsonschema:"skip missing media instead of failing"`
	// AllowOthersProgress 仅管理员可置位：允许导入包内他人的进度。
	AllowOthersProgress bool `json:"allow_others_progress,omitempty" jsonschema:"admin only: import progress that belongs to another user"`
	// ApplyWeights 缺省 false：只在 new_deck 目标下把包内权重写进新建的预设。
	ApplyWeights bool `json:"apply_weights,omitempty" jsonschema:"use the package's FSRS weights for the new deck's preset (new_deck only); default false"`
}

// updateNoteIn 是 update_note 的入参。
type updateNoteIn struct {
	NoteID string         `json:"note_id" jsonschema:"the public id of the note to update"`
	Kind   string         `json:"kind,omitempty" jsonschema:"card type; defaults to the existing kind"`
	Fields map[string]any `json:"fields" jsonschema:"new field values"`
	Tags   []string       `json:"tags,omitempty" jsonschema:"replacement tags"`
}

// deleteNoteIn 是 delete_note 的入参。
type deleteNoteIn struct {
	NoteID string `json:"note_id" jsonschema:"the public id of the note to soft-delete"`
}

// getDueCardsIn 是 get_due_cards 的入参。
// deck_id 与 deck_ids 互斥：同时给出返回参数错误；两者都缺省＝全部卡组。
type getDueCardsIn struct {
	DeckID  string   `json:"deck_id,omitempty" jsonschema:"public id of a single deck to scope the queue; empty means all decks; mutually exclusive with deck_ids"`
	DeckIDs []string `json:"deck_ids,omitempty" jsonschema:"public ids of the decks to scope the queue; absent or empty means all decks; mutually exclusive with deck_id"`
	Tags    []string `json:"tags,omitempty" jsonschema:"only cards whose note carries any of these tags; requires exactly one deck (deck_id, or deck_ids with one entry)"`
	Limit   int      `json:"limit,omitempty" jsonschema:"maximum cards to return (1..500)"`
}

// listDeckTagsIn 是 list_deck_tags 的入参。
type listDeckTagsIn struct {
	DeckID string `json:"deck_id" jsonschema:"public id of the deck whose tags to list"`
}

// submitReviewIn 是 submit_review 的入参。规则与 REST 相同（见 api.SubmitReviewInput）：
// 自评题型给 rating，作答类题型给 answer 或 give_up；grade_source 只接受 self。
type submitReviewIn struct {
	CardID          string `json:"card_id" jsonschema:"the public id of the card being reviewed"`
	Rating          int    `json:"rating,omitempty" jsonschema:"self-assessed rating 1=again, 2=hard, 3=good, 4=easy; omit for card types graded by the server (typed, numeric, choice_single, choice_multi, true_false)"`
	Answer          any    `json:"answer,omitempty" jsonschema:"the answer for a card type graded by the server: a string for typed, a string or number for numeric, an option index for choice_single, an array of option indices for choice_multi, a boolean for true_false, an array of strings for cloze (one per blank of the card's cloze number, in order of appearance), an array of strings for list (the recalled items; in item order when the note is ordered); for short_answer an optional string the learner wrote, sent together with the self rating and stored with the review"`
	GiveUp          bool   `json:"give_up,omitempty" jsonschema:"give up on a card type graded by the server; records Again without grading"`
	ExpectedVersion int    `json:"expected_version,omitempty" jsonschema:"card state version the caller read"`
	ElapsedMS       *int   `json:"elapsed_ms,omitempty" jsonschema:"time spent on the card in milliseconds"`
	GradeSource     string `json:"grade_source,omitempty" jsonschema:"must be omitted or self; the server decides where a grade came from"`
}

// ---- 处理器 ----

func (s *Server) listDecks(ctx context.Context, id Identity, _ listDecksIn) (any, error) {
	decks, err := s.api.ListDecks(ctx, id.User.ID)
	if err != nil {
		return nil, err
	}
	out := make([]api.DeckResponse, 0, len(decks))
	for _, d := range decks {
		out = append(out, s.api.ToDeckResponse(ctx, id.User.ID, d.Deck, d.Role))
	}
	return map[string]any{"decks": out}, nil
}

// createDeck 建一个空卡组：与 REST `POST /decks` 走同一 service 方法。
// 返回与 REST 相同的 api.DeckResponse，保证两种传输的响应形态不会漂移。
func (s *Server) createDeck(ctx context.Context, id Identity, in createDeckIn) (any, error) {
	// preset_id 是对外 id：空串交给 service 取缺省预设；非空则先解析成主键再传数字。
	var presetID uint64
	if pid := strings.TrimSpace(in.PresetID); pid != "" {
		p, err := s.api.PresetByPublicID(ctx, pid)
		if err != nil {
			return nil, err
		}
		presetID = p.ID
	}
	d, err := s.api.CreateDeck(ctx, id.User, api.CreateDeckInput{
		Name:        in.Name,
		Description: in.Description,
		PresetID:    presetID,
		APIKeyID:    id.apiKeyID(),
	})
	if err != nil {
		return nil, err
	}
	return s.api.ToDeckResponse(ctx, id.User.ID, *d, store.RoleOwner), nil
}

// updateDeck 修改卡组的名称与描述；与 REST `PATCH /decks/:id` 走同一 service 方法（仅 owner）。
func (s *Server) updateDeck(ctx context.Context, id Identity, in updateDeckIn) (any, error) {
	d, err := s.api.DeckByPublicID(ctx, in.DeckID)
	if err != nil {
		return nil, err
	}
	updated, err := s.api.UpdateDeck(ctx, id.User, d.ID, api.UpdateDeckInput{
		Name:        in.Name,
		Description: in.Description,
		APIKeyID:    id.apiKeyID(),
	})
	if err != nil {
		return nil, err
	}
	return s.api.ToDeckResponse(ctx, id.User.ID, *updated, store.RoleOwner), nil
}

func (s *Server) searchNotes(ctx context.Context, id Identity, in searchNotesIn) (any, error) {
	d, err := s.api.DeckByPublicID(ctx, in.DeckID)
	if err != nil {
		return nil, err
	}
	opts := store.NormalizeNoteListOptions(store.NoteListOptions{
		Page:    in.Page,
		PerPage: in.PerPage,
		Query:   in.Query,
		Tag:     in.Tag,
		Kind:    in.Kind,
		Status:  in.Status,
	})
	notes, total, err := s.api.ListNotes(ctx, id.User.ID, d.ID, opts)
	if err != nil {
		return nil, err
	}
	out := s.api.NotesJSON(ctx, id.User.ID, notes)
	return map[string]any{"notes": out, "total": total, "page": opts.Page, "per_page": opts.PerPage}, nil
}

// listDeckTags 列出卡组内的标签及各自的 note 数；与 REST `GET /decks/:id/tags` 走同一 service 方法。
func (s *Server) listDeckTags(ctx context.Context, id Identity, in listDeckTagsIn) (any, error) {
	d, err := s.api.DeckByPublicID(ctx, in.DeckID)
	if err != nil {
		return nil, err
	}
	tags, err := s.api.DeckTags(ctx, id.User.ID, d.ID)
	if err != nil {
		return nil, err
	}
	return map[string]any{"tags": tags}, nil
}

// getNote 按对外 id 读取单条 note；与 REST `GET /notes/:id` 走同一 service 方法。
func (s *Server) getNote(ctx context.Context, id Identity, in getNoteIn) (any, error) {
	n, err := s.api.GetNote(ctx, id.User.ID, in.NoteID)
	if err != nil {
		return nil, err
	}
	return s.api.NotesJSON(ctx, id.User.ID, []store.Note{*n})[0], nil
}

// listCardTypes 列出题型自描述：与 REST `GET /card-types` 走同一 service 方法。
func (s *Server) listCardTypes(_ context.Context, _ Identity, _ listCardTypesIn) (api.CardTypesResponse, error) {
	return s.api.CardTypes(), nil
}

func (s *Server) getStats(ctx context.Context, id Identity, _ getStatsIn) (any, error) {
	return s.api.Stats(ctx, id.User)
}

// exportDeck 导出卡组包：与 REST `GET /decks/:id/package` 走同一 service 方法。
// 返回包内逻辑内容的 JSON 文档形态，可直接对照 schema/deck-package.schema.json 校验。
func (s *Server) exportDeck(ctx context.Context, id Identity, in exportDeckIn) (any, error) {
	d, err := s.api.DeckByPublicID(ctx, in.DeckID)
	if err != nil {
		return nil, err
	}
	includeMedia := true
	if in.IncludeMedia != nil {
		includeMedia = *in.IncludeMedia
	}
	pkg, err := s.api.ExportDeckPackage(ctx, id.User.ID, d.ID, store.PackageOptions{
		IncludeProgress: in.IncludeProgress,
		IncludeMedia:    includeMedia,
		IncludeReviews:  in.IncludeReviews,
		IncludeWeights:  in.IncludeWeights,
	})
	if err != nil {
		return nil, err
	}
	return pkg.Document(), nil
}

func (s *Server) createNotes(ctx context.Context, id Identity, in bulkNotesIn) (any, error) {
	d, err := s.api.DeckByPublicID(ctx, in.DeckID)
	if err != nil {
		return nil, err
	}
	return s.api.ImportNotes(ctx, id.User.ID, d.ID, id.apiKeyID(), toImportRequest(in))
}

// bulkNotes 对一组 note 执行批量动作：与 REST `POST /notes/bulk` 走同一 service 方法。
// note_ids 是 note 的对外 id，解析与逐行判权都在 service 层。
func (s *Server) bulkNotes(ctx context.Context, id Identity, in bulkActionIn) (any, error) {
	return s.api.BulkNotes(ctx, id.User.ID, id.apiKeyID(), api.BulkNotesInput{
		Action:  in.Action,
		NoteIDs: in.NoteIDs,
		Tags:    in.Tags,
		DryRun:  in.DryRun,
	})
}

// importDeck 导入卡组包：与 REST `POST /decks/import`（或 `POST /decks/import-url`）
// 走同一 service 方法。包的权限判定、进度归属与审计都在 service 层完成；
// target 里的 into_deck:<public id> / replace_deck:<public id> 由 service 自行解析。
// package 与 url 互斥：二选一，缺一报参数错误。
func (s *Server) importDeck(ctx context.Context, id Identity, in importDeckIn) (any, error) {
	opts := store.PackageImportOptions{
		Target:              in.Target,
		DryRun:              in.DryRun,
		OnConflict:          in.OnConflict,
		SkipMissingMedia:    in.SkipMissingMedia,
		AllowOthersProgress: in.AllowOthersProgress,
		ApplyWeights:        in.ApplyWeights,
	}
	url := strings.TrimSpace(in.URL)
	if url != "" {
		if in.Package != nil {
			return nil, api.InvalidRequest("package and url are mutually exclusive")
		}
		return s.api.ImportDeckPackageURL(ctx, id.User, id.apiKeyID(), url, opts)
	}
	if in.Package == nil {
		return nil, api.InvalidRequest("package or url is required")
	}
	r, err := store.PackageReader(in.Package)
	if err != nil {
		return nil, err
	}
	return s.api.ImportDeckPackage(ctx, id.User, id.apiKeyID(), r, opts)
}

// createImportUpload 签发一次性上传票据：与 REST `POST /decks/import-uploads` 走同一 service 方法。
// 包字节随后由 agent 直接 PUT 到返回的 URL，不经过模型输出。
func (s *Server) createImportUpload(ctx context.Context, id Identity, in createImportUploadIn) (*api.ImportUpload, error) {
	return s.api.CreateImportUpload(ctx, id.User, id.apiKeyID(), store.PackageImportOptions{
		Target:              in.Target,
		DryRun:              in.DryRun,
		OnConflict:          in.OnConflict,
		SkipMissingMedia:    in.SkipMissingMedia,
		AllowOthersProgress: in.AllowOthersProgress,
		ApplyWeights:        in.ApplyWeights,
	})
}

func (s *Server) updateNote(ctx context.Context, id Identity, in updateNoteIn) (any, error) {
	n, err := s.api.NoteByPublicID(ctx, in.NoteID)
	if err != nil {
		return nil, err
	}
	var tags *[]string
	if in.Tags != nil {
		t := in.Tags
		tags = &t
	}
	updated, err := s.api.UpdateNote(ctx, id.User.ID, n.ID, id.apiKeyID(), api.UpdateNoteInput{
		Kind:   in.Kind,
		Fields: in.Fields,
		Tags:   tags,
	})
	if err != nil {
		return nil, err
	}
	return s.api.NotesJSON(ctx, id.User.ID, []store.Note{*updated})[0], nil
}

func (s *Server) deleteNote(ctx context.Context, id Identity, in deleteNoteIn) (any, error) {
	n, err := s.api.NoteByPublicID(ctx, in.NoteID)
	if err != nil {
		return nil, err
	}
	if _, err := s.api.DeleteNote(ctx, id.User.ID, n.ID, id.apiKeyID()); err != nil {
		return nil, err
	}
	return map[string]any{"deleted": true, "id": n.PublicID}, nil
}

func (s *Server) getDueCards(ctx context.Context, id Identity, in getDueCardsIn) (any, error) {
	// deck_id 与 deck_ids 互斥：同时给出是调用方的参数错误，经同一 MCP 错误出口返回
	// （客户端看到 isErr 与稳定的 invalid_request code）。空串视为未提供。
	deckID := strings.TrimSpace(in.DeckID)
	if deckID != "" && len(in.DeckIDs) > 0 {
		return nil, api.InvalidRequest("deck_id and deck_ids are mutually exclusive")
	}
	// 对外 id 逐个解析成主键：任一个未知即整次调用失败（不静默过滤），与 REST 同口径。
	// 空串条目直接跳过（与 REST 跳过空 deck 查询参数一致）。
	var resolved []uint64
	switch {
	case deckID != "":
		d, err := s.api.DeckByPublicID(ctx, deckID)
		if err != nil {
			return nil, err
		}
		resolved = []uint64{d.ID}
	default:
		for _, raw := range in.DeckIDs {
			pid := strings.TrimSpace(raw)
			if pid == "" {
				continue
			}
			d, err := s.api.DeckByPublicID(ctx, pid)
			if err != nil {
				return nil, err
			}
			resolved = append(resolved, d.ID)
		}
	}
	cards, err := s.api.DueCards(ctx, id.User, resolved, in.Tags, in.Limit)
	if err != nil {
		return nil, err
	}
	return map[string]any{"cards": cards}, nil
}

func (s *Server) submitReview(ctx context.Context, id Identity, in submitReviewIn) (any, error) {
	if in.GradeSource != "" && in.GradeSource != "self" {
		return nil, api.InvalidRequest("grade_source is decided by the server")
	}
	var answer json.RawMessage
	if in.Answer != nil {
		raw, err := json.Marshal(in.Answer)
		if err != nil {
			return nil, api.InvalidRequest("answer is not valid JSON")
		}
		answer = raw
	}
	return s.api.SubmitReview(ctx, id.User, id.apiKeyID(), api.SubmitReviewInput{
		CardID:          in.CardID,
		Rating:          in.Rating,
		Answer:          answer,
		GiveUp:          in.GiveUp,
		ExpectedVersion: in.ExpectedVersion,
		ElapsedMS:       in.ElapsedMS,
	})
}

// toImportRequest 把 MCP 入参转成 service 层的 ImportRequest；两种工具共用。
func toImportRequest(in bulkNotesIn) api.ImportRequest {
	notes := make([]api.ImportNote, 0, len(in.Notes))
	for _, n := range in.Notes {
		notes = append(notes, api.ImportNote{
			Kind:        n.Kind,
			Fields:      n.Fields,
			ExternalRef: n.ExternalRef,
			NoteID:      n.NoteID,
			Tags:        n.Tags,
		})
	}
	return api.ImportRequest{DryRun: in.DryRun, OnConflict: in.OnConflict, Notes: notes}
}
