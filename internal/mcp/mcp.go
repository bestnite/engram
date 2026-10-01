// Package mcp 提供内置 MCP server：同进程 HTTP（POST /mcp，streamable HTTP），
// 复用用户级 API Key 鉴权与 internal/api 的 service 层（DESIGN.md §7.4、AGENTS.md §5 M4-6/M4-7）。
//
// 只提供 HTTP，不提供 stdio（多用户服务没有“进程即身份”的语义）。
// 工具不封装业务逻辑，只做参数校验并调用与 REST 完全相同的方法。
package mcp

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"

	sdkmcp "github.com/modelcontextprotocol/go-sdk/mcp"

	"example.com/flashcard/internal/api"
	"example.com/flashcard/internal/store"
)

// version 由构建注入；未注入时为 dev，仅用于 MCP 实现信息。
var version = "dev"

// Deps 是 MCP server 的显式依赖：复用已装配好的 REST API（同一批 service 方法）。
type Deps struct {
	API    *api.API
	Logger *slog.Logger
}

// Server 持有 streamable HTTP handler；每个请求按其身份构建工具子集。
type Server struct {
	api     *api.API
	logger  *slog.Logger
	handler *sdkmcp.StreamableHTTPHandler
}

// New 构造 MCP server；API 必填（业务逻辑全部复用 api 的 service 层）。
func New(deps Deps) (*Server, error) {
	if deps.API == nil {
		return nil, errors.New("mcp: Deps.API is required")
	}
	logger := deps.Logger
	if logger == nil {
		logger = slog.Default()
	}
	s := &Server{api: deps.API, logger: logger}
	// getServer 每个 HTTP 请求调用一次：从请求上下文取身份，按 scope 构建工具集。
	// 这样 tools/list 在握手时即按 key 过滤，且 tools/call 的处理器闭包持有同一身份复查。
	s.handler = sdkmcp.NewStreamableHTTPHandler(func(r *http.Request) *sdkmcp.Server {
		id, _ := IdentityFrom(r.Context())
		return s.build(id)
	}, &sdkmcp.StreamableHTTPOptions{})
	return s, nil
}

// ServeHTTP 实现 http.Handler；调用方必须先完成鉴权并把身份写入请求上下文
// （见 WithIdentity），复用与 REST 相同的 API Key 校验、限流与审计。
func (s *Server) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	s.handler.ServeHTTP(w, r)
}

// ---- 身份上下文 ----

type identityKey struct{}

// Identity 是一次 MCP 请求的已认证身份：用户与（可空的）API key。
// 会话 cookie 通道下 Key 为 nil，权限边界与网页登录一致。
type Identity struct {
	User *store.User
	Key  *store.APIKey
}

// WithIdentity 把已认证身份注入请求上下文；由 web 装配层在鉴权中间件之后调用。
func WithIdentity(ctx context.Context, u *store.User, k *store.APIKey) context.Context {
	return context.WithValue(ctx, identityKey{}, Identity{User: u, Key: k})
}

// IdentityFrom 取回身份；缺失时 ok=false。
func IdentityFrom(ctx context.Context) (Identity, bool) {
	id, ok := ctx.Value(identityKey{}).(Identity)
	return id, ok && id.User != nil
}

// apiKeyID 返回当前 key 的 id；会话通道为 nil。
func (id Identity) apiKeyID() *uint64 {
	if id.Key != nil {
		return store.Ptr(id.Key.ID)
	}
	return nil
}

// hasScope 判断身份是否覆盖 scope：key 通道用 key.HasScope（admin 蕴含其余三档）；
// 会话通道与网页登录一致（登录用户可读可写可复习，admin 另需管理员角色）。
func hasScope(id Identity, scope string) bool {
	if id.Key != nil {
		return id.Key.HasScope(scope)
	}
	if id.User == nil {
		return false
	}
	if scope == store.ScopeAdmin {
		return id.User.Role == store.RoleAdmin
	}
	return true
}

// toolScopes 是工具名到所需 scope 的映射；握手过滤与调用复查共用，保证同一份规则。
var toolScopes = map[string]string{
	"list_decks":    store.ScopeRead,
	"search_notes":  store.ScopeRead,
	"get_stats":     store.ScopeRead,
	"export_deck":   store.ScopeRead,
	"create_notes":  store.ScopeWrite,
	"update_note":   store.ScopeWrite,
	"delete_note":   store.ScopeWrite,
	"import_deck":   store.ScopeWrite,
	"get_due_cards": store.ScopeReview,
	"submit_review": store.ScopeReview,
}

const instructions = "Flashcard library and scheduler. Tools mirror the REST /api/v1 surface " +
	"and are filtered by the API key's scopes (read/write/review)."

// build 为一次请求构建工具集：注册全部工具（供“按名字硬调”返回权限错误），
// 并在 tools/list 上按身份过滤，保证客户端看到的列表就是它真能用的集合。
func (s *Server) build(id Identity) *sdkmcp.Server {
	srv := sdkmcp.NewServer(&sdkmcp.Implementation{Name: "flashcard", Version: version}, &sdkmcp.ServerOptions{
		Instructions: instructions,
	})
	srv.AddReceivingMiddleware(s.filterTools(id))

	addTool(srv, id, "list_decks", "List the caller's decks.", s.listDecks)
	addTool(srv, id, "search_notes", "Search notes in a deck (pagination, tag and keyword filters).", s.searchNotes)
	addTool(srv, id, "get_stats", "Summary statistics: due count, reviews, retention, notes and cards.", s.getStats)
	addTool(srv, id, "export_deck", "Export card-level rows for one deck (or all decks) as JSON.", s.exportDeck)
	addTool(srv, id, "create_notes", "Bulk create/update notes in a deck (idempotent by external_ref; supports dry_run).", s.createNotes)
	addTool(srv, id, "update_note", "Update one note's content and tags.", s.updateNote)
	addTool(srv, id, "delete_note", "Soft-delete one note (review progress is preserved).", s.deleteNote)
	addTool(srv, id, "import_deck", "Import notes into a deck. This revision accepts the same M4-3 bulk structure as create_notes (deck_id + notes); full .fdeck package import is M5.", s.importDeck)
	addTool(srv, id, "get_due_cards", "Return cards due for review, including their source fields.", s.getDueCards)
	addTool(srv, id, "submit_review", "Submit a review rating for a card (1..4) with optimistic version check.", s.submitReview)

	return srv
}

// filterTools 在 tools/list 结果上按身份过滤工具；每次请求独立，不会泄漏越权工具。
func (s *Server) filterTools(id Identity) sdkmcp.Middleware {
	return func(next sdkmcp.MethodHandler) sdkmcp.MethodHandler {
		return func(ctx context.Context, method string, req sdkmcp.Request) (sdkmcp.Result, error) {
			res, err := next(ctx, method, req)
			if err != nil || method != "tools/list" {
				return res, err
			}
			lt, ok := res.(*sdkmcp.ListToolsResult)
			if !ok {
				return res, err
			}
			kept := make([]*sdkmcp.Tool, 0, len(lt.Tools))
			for _, t := range lt.Tools {
				scope, known := toolScopes[t.Name]
				if known && hasScope(id, scope) {
					kept = append(kept, t)
				}
			}
			lt.Tools = kept
			return lt, nil
		}
	}
}

// addTool 注册一个工具：调用前必须复查身份是否仍持有该工具所需的 scope。
// 缺 scope 时返回 tool error（置 IsError），而不是把工具从注册表里摘掉 —— 按名字硬调也能得到权限错误。
func addTool[In, Out any](s *sdkmcp.Server, id Identity, name, desc string, fn func(context.Context, Identity, In) (Out, error)) {
	scope := toolScopes[name]
	tool := &sdkmcp.Tool{Name: name, Description: desc}
	sdkmcp.AddTool[In, Out](s, tool, func(ctx context.Context, _ *sdkmcp.CallToolRequest, in In) (*sdkmcp.CallToolResult, Out, error) {
		var zero Out
		if !hasScope(id, scope) {
			return nil, zero, fmt.Errorf("scope_required: api key is missing the required scope: %s", scope)
		}
		out, err := fn(ctx, id, in)
		if err != nil {
			return nil, zero, err
		}
		return nil, out, nil
	})
}
