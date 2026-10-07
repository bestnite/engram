package auth

import (
	"context"
	"errors"

	"git.nite07.com/nite/engram/internal/store"
)

// 权限判定的两个哨兵错误；调用方据此决定 HTTP 状态与稳定 code：
//   - ErrDeckNotFound：卡组不存在 —— 404 / not_found
//   - ErrForbidden：无访问权或角色不够 —— 403（有角色但不满足时为 insufficient_role）
var (
	// ErrDeckNotFound 表示卡组不存在（或对当前用户不可见）。
	ErrDeckNotFound = errors.New("deck not found")
	// ErrForbidden 表示用户对该卡组没有所需权限。
	ErrForbidden = errors.New("forbidden")
)

// DeckAccess 是 Web 与 REST/MCP 共用的卡组权限判定助手（单一实现）。
//
// 有效角色解析顺序：
//  1. deck_grants 里的显式授权行；
//  2. 没有授权行但 decks.owner_user_id == userID 时视为 owner —— M2 建的卡组没有授权行，
//     owner 列是唯一真相，这条回落保证升级后既有卡组仍归原主。
//  3. 其余为“无任何访问权”（空角色）。
//
// 所有涉及卡组的 handler 都必须经这里判定；判定只有一份，两个传输不各自实现。
type DeckAccess struct {
	decks  *store.DeckStore
	grants *store.GrantStore
}

// NewDeckAccess 构造判定器；grants 可为 nil（未接授权存储时仅靠 owner 列）。
func NewDeckAccess(decks *store.DeckStore, grants *store.GrantStore) *DeckAccess {
	return &DeckAccess{decks: decks, grants: grants}
}

// Role 返回用户在卡组上的有效角色；无访问权时返回空串。
// 卡组不存在返回 ErrDeckNotFound，避免调用方把“不存在”与“无授权”混为一谈。
func (d *DeckAccess) Role(ctx context.Context, deckID, userID uint64) (string, error) {
	deck, err := d.decks.ByID(ctx, deckID)
	if err != nil {
		return "", ErrDeckNotFound
	}
	if deck.OwnerUserID == userID {
		return store.RoleOwner, nil
	}
	if d.grants == nil {
		return "", nil
	}
	role, err := d.grants.Role(ctx, deckID, userID)
	if err != nil {
		return "", err
	}
	if !store.ValidRole(role) {
		return visibilityRole(deck), nil
	}
	return role, nil
}

// visibilityRole 把卡组可见性折算成“隐式只读”：public 与 unlisted 都允许任何登录用户
// 以 reader 身份访问内容（public 登录用户可见，unlisted 拿到链接可看）。
// 它只授 reader，因此看不到内容的人也无法借可见性获得写权限 —— 写入仍要求 editor/owner。
// private（含空串，兼容 M2 建的老行）不授任何权限。
func visibilityRole(deck *store.Deck) string {
	switch deck.Visibility {
	case store.DeckVisibilityPublic, store.DeckVisibilityUnlisted:
		return store.RoleReader
	default:
		return ""
	}
}

// RequireRole 要求用户在卡组上至少拥有 want 角色，返回命中的卡组与该用户的有效角色。
//
// want 非法（不是三个角色之一）时按“拒绝一切”处理：拼错角色名应当是拒绝，而不是放行。
// 无访问权与角色不够都返回 ErrForbidden；调用方可用返回的 role 是否为空区分二者，
// 以给出 forbidden / insufficient_role 两个稳定 code。
func (d *DeckAccess) RequireRole(ctx context.Context, deckID, userID uint64, want string) (*store.Deck, string, error) {
	deck, err := d.decks.ByID(ctx, deckID)
	if err != nil {
		return nil, "", ErrDeckNotFound
	}
	role := ""
	if deck.OwnerUserID == userID {
		role = store.RoleOwner
	} else if d.grants != nil {
		granted, gerr := d.grants.Role(ctx, deckID, userID)
		if gerr != nil {
			return nil, "", gerr
		}
		if store.ValidRole(granted) {
			role = granted
		} else {
			role = visibilityRole(deck)
		}
	} else {
		role = visibilityRole(deck)
	}
	if !store.RoleAllows(role, want) {
		return nil, role, ErrForbidden
	}
	return deck, role, nil
}
