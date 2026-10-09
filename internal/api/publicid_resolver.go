package api

import (
	"context"
	"net/http"

	"git.nite07.com/nite/engram/internal/store"
)

// 本文件把对外 id（不透明字符串）解析成数字主键，供 REST 之外的调用方复用。
//
// 约定「运输层解析、业务层保持数字」：内置 MCP 工具拿到公共 id 后先在这里换成主键，
// 再调用与 REST 完全相同的 service 方法，避免 REST 与 MCP 各写一份解析。解析失败一律
// 映射成与 REST 相同的 404 not_found（空串同样视为未找到，绝不让空串匹配到某一行）。

// DeckByPublicID 按对外 id 解析卡组；未知、空串或非法一律返回 404 not_found。
func (a *API) DeckByPublicID(ctx context.Context, publicID string) (*store.Deck, error) {
	d, err := a.decks.ByPublicID(ctx, publicID)
	if err != nil {
		return nil, newServiceError(http.StatusNotFound, CodeNotFound, "")
	}
	return d, nil
}

// NoteByPublicID 按对外 id 解析 note；未知、空串或已软删一律返回 404 not_found。
func (a *API) NoteByPublicID(ctx context.Context, publicID string) (*store.Note, error) {
	n, err := a.notes.ByPublicID(ctx, publicID)
	if err != nil {
		return nil, newServiceError(http.StatusNotFound, CodeNotFound, "")
	}
	return n, nil
}

// PresetByPublicID 按对外 id 解析预设；未知或空串返回 404 not_found。
func (a *API) PresetByPublicID(ctx context.Context, publicID string) (*store.Preset, error) {
	p, err := a.presets.ByPublicID(ctx, publicID)
	if err != nil {
		return nil, newServiceError(http.StatusNotFound, CodeNotFound, "")
	}
	return p, nil
}
