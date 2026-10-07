package mcp

import (
	"context"

	"git.nite07.com/nite/engram/internal/api"
	"git.nite07.com/nite/engram/internal/store"
)

// 本文件定义九个 MCP 工具的入参类型与处理器；每个处理器只做参数整形，
// 随后调用与同名 REST 端点完全相同的 internal/api service 方法。

// ---- 入参 ----

// listDecksIn 无参数。
type listDecksIn struct{}

// createDeckIn 是 create_deck 的入参；preset_id 为 0 时使用（或创建）调用者的 Default 预设。
type createDeckIn struct {
	Name        string `json:"name" jsonschema:"the deck name (required)"`
	Description string `json:"description,omitempty" jsonschema:"optional deck description"`
	Visibility  string `json:"visibility,omitempty" jsonschema:"visibility: private (default), unlisted or public"`
	PresetID    uint64 `json:"preset_id,omitempty" jsonschema:"scheduling preset id; 0 uses the caller's Default preset"`
}

// searchNotesIn 是 search_notes 的入参；deck_id 必填。
type searchNotesIn struct {
	DeckID  uint64 `json:"deck_id" jsonschema:"the deck to search in"`
	Query   string `json:"q,omitempty" jsonschema:"keyword matched against note fields"`
	Tag     string `json:"tag,omitempty" jsonschema:"exact tag filter"`
	Kind    string `json:"kind,omitempty" jsonschema:"card type filter (e.g. basic, cloze)"`
	Status  string `json:"status,omitempty" jsonschema:"note status filter"`
	Page    int    `json:"page,omitempty" jsonschema:"page number, 1-based"`
	PerPage int    `json:"per_page,omitempty" jsonschema:"items per page"`
}

// getStatsIn 无参数。
type getStatsIn struct{}

// exportDeckIn 是 export_deck 的入参；它导出一个**卡组包**。
type exportDeckIn struct {
	DeckID          uint64 `json:"deck_id" jsonschema:"the deck to export as a package"`
	IncludeProgress bool   `json:"include_progress,omitempty" jsonschema:"include the caller's own review progress"`
	// IncludeMedia 缺省为 true（媒体默认内联）。
	IncludeMedia   *bool `json:"include_media,omitempty" jsonschema:"inline media bytes; default true"`
	IncludeReviews bool  `json:"include_reviews,omitempty" jsonschema:"include review logs (requires include_progress)"`
}

// importNoteIn 是单个 note 的入参，字段名与 schema/note-import.schema.json 一致。
type importNoteIn struct {
	Kind        string         `json:"kind" jsonschema:"card type (e.g. basic, cloze)"`
	Fields      map[string]any `json:"fields" jsonschema:"field values for the card type"`
	ExternalRef string         `json:"external_ref,omitempty" jsonschema:"caller-defined idempotency key, unique per deck"`
	// NoteID 按主键寻址已有 note 就地改写；与 external_ref 互斥（M4-12）。
	NoteID uint64   `json:"note_id,omitempty" jsonschema:"address an existing note by primary key to rewrite in place; mutually exclusive with external_ref"`
	Tags   []string `json:"tags,omitempty" jsonschema:"note tags"`
}

// bulkNotesIn 是 create_notes 的入参（M4-3 批量建卡路径）。
type bulkNotesIn struct {
	DeckID     uint64         `json:"deck_id" jsonschema:"target deck"`
	Notes      []importNoteIn `json:"notes" jsonschema:"notes to create or update (1..500)"`
	DryRun     bool           `json:"dry_run,omitempty" jsonschema:"validate and count without writing"`
	OnConflict string         `json:"on_conflict,omitempty" jsonschema:"conflict policy: skip, update (default) or fail"`
}

// bulkActionIn 是 bulk_notes 的入参，与 REST 的 POST /notes/bulk 请求体同形
// （MCP 只做参数整形，语义与校验都在 service 层）。
type bulkActionIn struct {
	Action  string   `json:"action" jsonschema:"bulk action: delete, add_tags, remove_tags or set_tags"`
	NoteIDs []uint64 `json:"note_ids" jsonschema:"notes to act on, deduplicated to 1..500 entries"`
	Tags    []string `json:"tags,omitempty" jsonschema:"tags for the tag actions, 1..20 entries; not allowed for delete"`
	DryRun  bool     `json:"dry_run,omitempty" jsonschema:"count without writing or auditing"`
}

// importDeckIn 是 import_deck 的入参：接受一个卡组包。
type importDeckIn struct {
	// Package 是包本体：export_deck 输出的 JSON 文档，或 base64 编码的 .edeck zip。
	Package any `json:"package" jsonschema:"the deck package: export_deck's JSON document, or a base64-encoded .edeck archive"`
	// Target 取值 new_deck（默认）、into_deck:<id>、replace_deck:<id>。
	Target     string `json:"target,omitempty" jsonschema:"import target: new_deck (default), into_deck:<id> or replace_deck:<id>"`
	DryRun     bool   `json:"dry_run,omitempty" jsonschema:"validate and count without writing"`
	OnConflict string `json:"on_conflict,omitempty" jsonschema:"conflict policy: skip, update (default) or fail"`
	// SkipMissingMedia 缺失媒体时只计数并继续；默认 false（缺媒体即失败）。
	SkipMissingMedia bool `json:"skip_missing_media,omitempty" jsonschema:"skip missing media instead of failing"`
	// AllowOthersProgress 仅管理员可置位：允许导入包内他人的进度。
	AllowOthersProgress bool `json:"allow_others_progress,omitempty" jsonschema:"admin only: import progress that belongs to another user"`
}

// updateNoteIn 是 update_note 的入参。
type updateNoteIn struct {
	NoteID uint64         `json:"note_id" jsonschema:"the note to update"`
	Kind   string         `json:"kind,omitempty" jsonschema:"card type; defaults to the existing kind"`
	Fields map[string]any `json:"fields" jsonschema:"new field values"`
	Tags   []string       `json:"tags,omitempty" jsonschema:"replacement tags"`
}

// deleteNoteIn 是 delete_note 的入参。
type deleteNoteIn struct {
	NoteID uint64 `json:"note_id" jsonschema:"the note to soft-delete"`
}

// getDueCardsIn 是 get_due_cards 的入参。
// deck_id 与 deck_ids 互斥：同时给出返回参数错误；两者都缺省＝全部卡组。
type getDueCardsIn struct {
	DeckID  uint64   `json:"deck_id,omitempty" jsonschema:"single deck to scope the queue; 0 or absent means all decks; mutually exclusive with deck_ids"`
	DeckIDs []uint64 `json:"deck_ids,omitempty" jsonschema:"set of decks to scope the queue; absent or empty means all decks; mutually exclusive with deck_id"`
	Limit   int      `json:"limit,omitempty" jsonschema:"maximum cards to return (1..500)"`
}

// submitReviewIn 是 submit_review 的入参。
type submitReviewIn struct {
	CardID          uint64 `json:"card_id" jsonschema:"the card being reviewed"`
	Rating          int    `json:"rating" jsonschema:"rating 1=again, 2=hard, 3=good, 4=easy"`
	ExpectedVersion int    `json:"expected_version,omitempty" jsonschema:"card state version the caller read"`
	ElapsedMS       *int   `json:"elapsed_ms,omitempty" jsonschema:"time spent on the card in milliseconds"`
	GradeSource     string `json:"grade_source,omitempty" jsonschema:"where the grade came from"`
}

// ---- 处理器 ----

func (s *Server) listDecks(ctx context.Context, id Identity, _ listDecksIn) (any, error) {
	decks, err := s.api.ListDecks(ctx, id.User.ID)
	if err != nil {
		return nil, err
	}
	out := make([]api.DeckResponse, 0, len(decks))
	for _, d := range decks {
		out = append(out, api.ToDeckResponse(d))
	}
	return map[string]any{"decks": out}, nil
}

// createDeck 建一个空卡组：与 REST `POST /decks` 走同一 service 方法。
// 返回与 REST 相同的 api.DeckResponse，保证两种传输的响应形态不会漂移。
func (s *Server) createDeck(ctx context.Context, id Identity, in createDeckIn) (any, error) {
	d, err := s.api.CreateDeck(ctx, id.User, api.CreateDeckInput{
		Name:        in.Name,
		Description: in.Description,
		Visibility:  in.Visibility,
		PresetID:    in.PresetID,
		APIKeyID:    id.apiKeyID(),
	})
	if err != nil {
		return nil, err
	}
	return api.ToDeckResponse(*d), nil
}

func (s *Server) searchNotes(ctx context.Context, id Identity, in searchNotesIn) (any, error) {
	opts := store.NormalizeNoteListOptions(store.NoteListOptions{
		Page:    in.Page,
		PerPage: in.PerPage,
		Query:   in.Query,
		Tag:     in.Tag,
		Kind:    in.Kind,
		Status:  in.Status,
	})
	notes, total, err := s.api.ListNotes(ctx, id.User.ID, in.DeckID, opts)
	if err != nil {
		return nil, err
	}
	out := make([]map[string]any, 0, len(notes))
	for i := range notes {
		out = append(out, api.NoteJSON(&notes[i]))
	}
	return map[string]any{"notes": out, "total": total, "page": opts.Page, "per_page": opts.PerPage}, nil
}

func (s *Server) getStats(ctx context.Context, id Identity, _ getStatsIn) (any, error) {
	return s.api.Stats(ctx, id.User)
}

// exportDeck 导出卡组包（M5-8）：与 REST `GET /decks/:id/package` 走同一 service 方法。
// 返回包内逻辑内容的 JSON 文档形态，可直接对照 schema/deck-package.schema.json 校验。
func (s *Server) exportDeck(ctx context.Context, id Identity, in exportDeckIn) (any, error) {
	includeMedia := true
	if in.IncludeMedia != nil {
		includeMedia = *in.IncludeMedia
	}
	pkg, err := s.api.ExportDeckPackage(ctx, id.User.ID, in.DeckID, in.IncludeProgress, includeMedia, in.IncludeReviews)
	if err != nil {
		return nil, err
	}
	return pkg.Document(), nil
}

func (s *Server) createNotes(ctx context.Context, id Identity, in bulkNotesIn) (any, error) {
	return s.api.ImportNotes(ctx, id.User.ID, in.DeckID, id.apiKeyID(), toImportRequest(in))
}

// bulkNotes 对一组 note 执行批量动作（M4-13）：与 REST `POST /notes/bulk` 走同一 service 方法。
func (s *Server) bulkNotes(ctx context.Context, id Identity, in bulkActionIn) (any, error) {
	return s.api.BulkNotes(ctx, id.User.ID, id.apiKeyID(), api.BulkNotesInput{
		Action:  in.Action,
		NoteIDs: in.NoteIDs,
		Tags:    in.Tags,
		DryRun:  in.DryRun,
	})
}

// importDeck 导入卡组包（M5-8）：与 REST `POST /decks/import` 走同一 service 方法。
// 包的权限判定、进度归属与审计都在 service 层（ImportDeckPackage）完成。
func (s *Server) importDeck(ctx context.Context, id Identity, in importDeckIn) (any, error) {
	r, err := store.PackageReader(in.Package)
	if err != nil {
		return nil, err
	}
	return s.api.ImportDeckPackage(ctx, id.User, id.apiKeyID(), r, store.PackageImportOptions{
		Target:              in.Target,
		DryRun:              in.DryRun,
		OnConflict:          in.OnConflict,
		SkipMissingMedia:    in.SkipMissingMedia,
		AllowOthersProgress: in.AllowOthersProgress,
	})
}

func (s *Server) updateNote(ctx context.Context, id Identity, in updateNoteIn) (any, error) {
	var tags *[]string
	if in.Tags != nil {
		t := in.Tags
		tags = &t
	}
	updated, err := s.api.UpdateNote(ctx, id.User.ID, in.NoteID, id.apiKeyID(), api.UpdateNoteInput{
		Kind:   in.Kind,
		Fields: in.Fields,
		Tags:   tags,
	})
	if err != nil {
		return nil, err
	}
	return api.NoteJSON(updated), nil
}

func (s *Server) deleteNote(ctx context.Context, id Identity, in deleteNoteIn) (any, error) {
	deleted, err := s.api.DeleteNote(ctx, id.User.ID, in.NoteID, id.apiKeyID())
	if err != nil {
		return nil, err
	}
	return map[string]any{"deleted": true, "id": deleted}, nil
}

func (s *Server) getDueCards(ctx context.Context, id Identity, in getDueCardsIn) (any, error) {
	// deck_id 与 deck_ids 互斥：同时给出是调用方的参数错误，经同一 MCP 错误出口返回
	// （客户端看到 isErr 与稳定的 invalid_request code）。
	if in.DeckID != 0 && len(in.DeckIDs) > 0 {
		return nil, api.InvalidRequest("deck_id and deck_ids are mutually exclusive")
	}
	var deckIDs []uint64
	if in.DeckID != 0 {
		deckIDs = []uint64{in.DeckID}
	} else if len(in.DeckIDs) > 0 {
		deckIDs = in.DeckIDs
	}
	cards, err := s.api.DueCards(ctx, id.User, deckIDs, in.Limit)
	if err != nil {
		return nil, err
	}
	return map[string]any{"cards": cards}, nil
}

func (s *Server) submitReview(ctx context.Context, id Identity, in submitReviewIn) (any, error) {
	return s.api.SubmitReview(ctx, id.User, id.apiKeyID(), api.SubmitReviewInput{
		CardID:          in.CardID,
		Rating:          in.Rating,
		ExpectedVersion: in.ExpectedVersion,
		ElapsedMS:       in.ElapsedMS,
		GradeSource:     in.GradeSource,
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
