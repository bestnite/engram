// Package mcp 提供内置 MCP server：同进程 HTTP（POST /mcp，streamable HTTP），
// 复用用户级 API Key 鉴权与 internal/api 的 service 层。
//
// 只提供 HTTP，不提供 stdio（多用户服务没有“进程即身份”的语义）。
// 工具不封装业务逻辑，只做参数校验并调用与 REST 完全相同的方法。
//
// /mcp 只接受 API key 通道：会话 cookie 是浏览器通道，与机器接口的撤销和作用域语义
// 不同，因此没有 key 的请求一律拒绝。每个请求都按它本次携带的 key 重算身份。
package mcp

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"sort"
	"strconv"
	"sync"
	"time"

	sdkmcp "github.com/modelcontextprotocol/go-sdk/mcp"

	"git.nite07.com/nite/engram/internal/api"
	"git.nite07.com/nite/engram/internal/store"
)

// version 由构建注入；未注入时为 dev，仅用于 MCP 实现信息。
var version = "dev"

// Deps 是 MCP server 的显式依赖：复用已装配好的 REST API（同一批 service 方法）。
type Deps struct {
	API    *api.API
	Logger *slog.Logger
}

// Server 持有 streamable HTTP handler；每个请求按其自身携带的 API key 重算身份与工具子集。
type Server struct {
	api     *api.API
	logger  *slog.Logger
	handler *sdkmcp.StreamableHTTPHandler

	// mu 保护 sessions。sessions 记录每个会话握手（initialize）时的用户与登记时刻，
	// 供后续请求校验会话属主（规则 2）。SDK 自己持有会话对象，这张表只额外记属主。
	// 表有容量上限（mcpSessionCap），超出时按登记时刻淘汰最旧条目，避免长期运行无界增长。
	mu       sync.Mutex
	sessions map[string]sessionEntry

	// clock 取当前时间；生产为 time.Now，测试可注入以获得确定的登记时刻。
	clock func() time.Time
}

// sessionEntry 是属主表里的一条会话记录：握手用户 + 登记时刻（seenAt 用于容量淘汰）。
type sessionEntry struct {
	userID uint64
	seenAt time.Time
}

// mcpSessionCap 是属主表的容量上限。客户端每开一个会话就登记一条，正常关闭（DELETE 2xx）
// 或会话已被 SDK 遗忘（404）时清理；上限是兜底，防止客户端从不发 DELETE 的路径把表撑到无界。
// 采用「淘汰最旧 seenAt」而非「整批丢弃」：新会话比旧会话更可能仍在使用，逐条按时间淘汰
// 保留的总是最近的一批活跃会话。
const mcpSessionCap = 4096

// New 构造 MCP server；API 必填（业务逻辑全部复用 api 的 service 层）。
func New(deps Deps) (*Server, error) {
	if deps.API == nil {
		return nil, errors.New("mcp: Deps.API is required")
	}
	logger := deps.Logger
	if logger == nil {
		logger = slog.Default()
	}
	s := &Server{api: deps.API, logger: logger, sessions: map[string]sessionEntry{}, clock: time.Now}
	// getServer 只在建立会话时被调用一次：从握手请求的上下文取身份，按当时 scope 构建
	// 工具集。会话内后续请求复用这个 Server，因此它闭包里的身份只作为属主与用户来源；
	// 工具可见性与调用权限每次请求另按本次 key 重算（见 requestIdentity）。
	s.handler = sdkmcp.NewStreamableHTTPHandler(func(r *http.Request) *sdkmcp.Server {
		id, _ := IdentityFrom(r.Context())
		return s.build(id)
	}, &sdkmcp.StreamableHTTPOptions{})
	return s, nil
}

// mcpSessionHeader 是 streamable HTTP 规范里传递会话号的请求/响应头。
const mcpSessionHeader = "Mcp-Session-Id"

// 下面两个头是本项目内部约定：ServeHTTP 每次请求按当前 key 写入，处理器据此重算身份。
// SDK 把当前 HTTP 请求的头随请求交给处理器（RequestExtra.Header），这是把“本次请求
// 实际携带的 key”送到处理器的通道。ServeHTTP 总是覆盖同名头，客户端无法伪造。
const (
	requestKeyIDHeader  = "X-Engram-Mcp-Api-Key-Id"
	requestScopesHeader = "X-Engram-Mcp-Scopes"
)

// ServeHTTP 实现 http.Handler；调用方必须先完成鉴权并把身份写入请求上下文
// （见 WithIdentity），复用与 REST 相同的 API Key 校验、限流与审计。
//
// 它额外强制两条规则：
//  1. /mcp 只接受 API Key：没有 key 的身份（会话 cookie 通道）一律 401。
//  2. 会话内每个请求的 key 属主必须等于握手用户：会话号只证明“同一个客户端”，
//     不证明“同一个用户”，否则 B 用自己的 key 加 A 的会话号就能借用 A 的身份。
func (s *Server) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	id, ok := IdentityFrom(r.Context())
	if !ok || id.KeyID == 0 {
		s.logger.Warn("mcp request rejected: an api key is required", "remote", r.RemoteAddr)
		http.Error(w, "api key required", http.StatusUnauthorized)
		return
	}
	sid := r.Header.Get(mcpSessionHeader)
	if sid != "" && s.sessionOwnerMismatch(sid, id.User.ID) {
		s.logger.Warn("mcp session owner mismatch",
			"session_id", sid, "key_id", id.KeyID, "user_id", id.User.ID)
		http.Error(w, "session user mismatch", http.StatusForbidden)
		return
	}
	// 规则 3：把本次请求的 key 身份带上。工具可见性与权限按本次 key 计算，而不是用
	// 握手时固化那份——同一用户换一把低 scope key 复用高 scope 会话时不会提权。
	r.Header.Set(requestKeyIDHeader, strconv.FormatUint(id.KeyID, 10))
	r.Header.Set(requestScopesHeader, id.Scopes)

	rec := &statusWriter{ResponseWriter: w}
	s.handler.ServeHTTP(rec, r)

	switch {
	case sid == "":
		// 新会话：握手成功的响应会带 Mcp-Session-Id，记下它的属主供后续请求校验。
		if newSID := rec.Header().Get(mcpSessionHeader); newSID != "" {
			s.rememberSession(newSID, id.User.ID)
		}
	case r.Method == http.MethodDelete && rec.status >= http.StatusOK && rec.status < http.StatusMultipleChoices:
		// 规则 4：客户端用 DELETE /mcp 正常关闭会话（2xx）时也要清掉属主记录。
		// 漏掉这条路径，会话条目只会在 404 时被清，长期运行无界增长（F10c）。
		s.forgetSession(sid)
	case rec.status == http.StatusNotFound:
		// 会话已不存在（过期或已被 DELETE）：顺手清掉属主记录，避免这张表无界增长。
		s.forgetSession(sid)
	}
}

// sessionOwnerMismatch 报告会话是否已登记且属主不是 userID。未登记的会话返回 false，
// 交给 SDK 按它原有的“会话不存在”行为处理。
func (s *Server) sessionOwnerMismatch(sid string, userID uint64) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	entry, known := s.sessions[sid]
	return known && entry.userID != userID
}

// now 返回当前时刻；clock 未注入时退回 time.Now（正常装配都经 New 注入真实时钟）。
func (s *Server) now() time.Time {
	if s.clock != nil {
		return s.clock()
	}
	return time.Now()
}

func (s *Server) rememberSession(sid string, userID uint64) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.sessions[sid] = sessionEntry{userID: userID, seenAt: s.now()}
	if len(s.sessions) > mcpSessionCap {
		s.evictOldestLocked(len(s.sessions) - mcpSessionCap)
	}
}

// evictOldestLocked 按 seenAt 从旧到新删掉 n 条；调用方必须已持有 s.mu。
func (s *Server) evictOldestLocked(n int) {
	if n <= 0 {
		return
	}
	type timedSID struct {
		sid string
		at  time.Time
	}
	entries := make([]timedSID, 0, len(s.sessions))
	for sid, e := range s.sessions {
		entries = append(entries, timedSID{sid, e.seenAt})
	}
	sort.Slice(entries, func(i, j int) bool { return entries[i].at.Before(entries[j].at) })
	if n > len(entries) {
		n = len(entries)
	}
	for _, e := range entries[:n] {
		delete(s.sessions, e.sid)
	}
}

func (s *Server) forgetSession(sid string) {
	s.mu.Lock()
	delete(s.sessions, sid)
	s.mu.Unlock()
}

// statusWriter 记录响应状态码，供 ServeHTTP 判断握手是否成功、会话是否已消失。
// Unwrap 让 http.NewResponseController(w).Flush() 仍能穿透到真实 ResponseWriter，
// 保证 SSE 流式响应不被破坏。
type statusWriter struct {
	http.ResponseWriter
	status int
}

func (w *statusWriter) WriteHeader(code int) {
	w.status = code
	w.ResponseWriter.WriteHeader(code)
}

func (w *statusWriter) Unwrap() http.ResponseWriter { return w.ResponseWriter }

// ---- 身份上下文 ----

type identityKey struct{}

// Identity 是一次 MCP 请求的已认证身份：用户与该请求携带的 API key 的授权信息。
// MCP 只接受 API Key 通道（见 ServeHTTP 规则 1），因此 KeyID 非 0 才是可用身份。
type Identity struct {
	User   *store.User
	KeyID  uint64 // 本次请求携带的 API key 主键；0 表示没有 key
	Scopes string // 该 key 的归一化 scopes 字符串
}

// WithIdentity 把已认证身份注入请求上下文；由 web 装配层在鉴权中间件之后调用。
// k 为 nil 表示会话 cookie 通道（此时没有 key 身份，MCP 会拒绝）。
func WithIdentity(ctx context.Context, u *store.User, k *store.APIKey) context.Context {
	id := Identity{User: u}
	if k != nil {
		id.KeyID, id.Scopes = k.ID, k.Scopes
	}
	return context.WithValue(ctx, identityKey{}, id)
}

// IdentityFrom 取回身份；缺失时 ok=false。
func IdentityFrom(ctx context.Context) (Identity, bool) {
	id, ok := ctx.Value(identityKey{}).(Identity)
	return id, ok && id.User != nil
}

// requestIdentity 返回本次请求实际使用的身份。
//
// SDK 只在建立会话时构建一次 Server，之后同一会话的所有请求都复用那个 Server 与它闭包
// 里的握手身份。因此工具处理器不能拿握手身份判断 scope，必须读本次请求带上来的 key
// （见 ServeHTTP 写入的请求头）。key 的属主已由 ServeHTTP 确认与握手用户一致，所以用户
// 直接沿用握手那份。请求头缺失或畸形时退回握手身份。
func (s *Server) requestIdentity(handshake Identity, req sdkmcp.Request) Identity {
	extra := req.GetExtra()
	if extra == nil || extra.Header == nil {
		return handshake
	}
	keyID, err := strconv.ParseUint(extra.Header.Get(requestKeyIDHeader), 10, 64)
	if err != nil || keyID == 0 {
		return handshake
	}
	return Identity{User: handshake.User, KeyID: keyID, Scopes: extra.Header.Get(requestScopesHeader)}
}

// apiKeyID 返回本次请求 key 的 id；无 key 时为 nil。
func (id Identity) apiKeyID() *uint64 {
	if id.KeyID == 0 {
		return nil
	}
	return store.Ptr(id.KeyID)
}

// hasScope 判断身份是否覆盖 scope：MCP 只有 key 通道，按 key 的 scopes 判断
// （admin 蕴含其余三档）。
func hasScope(id Identity, scope string) bool {
	if id.KeyID == 0 {
		return false
	}
	return store.HasScope(id.Scopes, scope)
}

// toolScopes 是工具名到所需 scope 的映射；握手过滤与调用复查共用，保证同一份规则。
var toolScopes = map[string]string{
	"list_decks":           store.ScopeRead,
	"search_notes":         store.ScopeRead,
	"list_deck_tags":       store.ScopeRead,
	"get_note":             store.ScopeRead,
	"get_stats":            store.ScopeRead,
	"list_card_types":      store.ScopeRead,
	"export_deck":          store.ScopeRead,
	"create_deck":          store.ScopeWrite,
	"update_deck":          store.ScopeWrite,
	"create_notes":         store.ScopeWrite,
	"update_note":          store.ScopeWrite,
	"delete_note":          store.ScopeWrite,
	"bulk_notes":           store.ScopeWrite,
	"import_deck":          store.ScopeWrite,
	"create_import_upload": store.ScopeWrite,
	"get_due_cards":        store.ScopeReview,
	"submit_review":        store.ScopeReview,
}

const instructions = "Engram library and scheduler. Tools mirror the REST /api/v1 surface " +
	"and are filtered by the API key's scopes (read/write/review). " +
	"Every tool argument is generated by the model, so do not pass a large deck package or a package " +
	"that already exists as a file through import_deck: call create_import_upload and send the file " +
	"to the returned upload_url with an HTTP client such as curl."

// build 为一次请求构建工具集：注册全部工具（供“按名字硬调”返回权限错误），
// 并在 tools/list 上按身份过滤，保证客户端看到的列表就是它真能用的集合。
func (s *Server) build(id Identity) *sdkmcp.Server {
	srv := sdkmcp.NewServer(&sdkmcp.Implementation{Name: "engram", Version: version}, &sdkmcp.ServerOptions{
		Instructions: instructions,
	})
	srv.AddReceivingMiddleware(s.filterTools(id))

	addTool(s, srv, id, "list_decks", "List the decks visible to the caller (owned, or granted by another user).", s.listDecks)
	addTool(s, srv, id, "create_deck", "Create an empty deck (name required; preset_id 0 uses the caller's Default preset).", s.createDeck)
	addTool(s, srv, id, "update_deck", "Update a deck's name and description (owner only).", s.updateDeck)
	addTool(s, srv, id, "search_notes", "Search notes in a deck (pagination, tag and keyword filters).", s.searchNotes)
	addTool(s, srv, id, "get_note", "Read one note by its public id.", s.getNote)
	addTool(s, srv, id, "list_deck_tags", "List the tags used in a deck with the number of notes carrying each; use them as the tags filter of get_due_cards.", s.listDeckTags)
	addTool(s, srv, id, "get_stats", "Summary statistics: due count, reviews, retention, notes and cards. retention is true retention: the share of due reviews (cards already in the Review state) not rated Again, over all history; retention_total and retention_passed are its denominator and numerator, and retention_total = 0 means there is no retention data yet.", s.getStats)
	addTool(s, srv, id, "list_card_types", "List the card types this instance supports. Each entry gives the kind, its fields (key, editor control, required, default), which fields form the front and back, whether the server grades typed answers, and an example note whose fields pass validation: copy its shape. Option indexes start at 0, and math is written with \\( \\) and \\[ \\], not $. Call it before writing notes for create_notes or a deck package.", s.listCardTypes)
	addTool(s, srv, id, "export_deck", "Export one deck as a self-contained deck package: manifest, notes, cards and preset as JSON, with optional progress and inlined media.", s.exportDeck)
	addTool(s, srv, id, "create_notes", "Bulk create/update notes in a deck (idempotent by external_ref; supports dry_run).", s.createNotes)
	addTool(s, srv, id, "update_note", "Update one note's content and tags.", s.updateNote)
	addTool(s, srv, id, "delete_note", "Soft-delete one note (review progress is preserved).", s.deleteNote)
	addTool(s, srv, id, "bulk_notes", "Apply one bulk action (delete, add_tags, remove_tags, set_tags) to a set of notes; dry_run counts without writing. Mirrors REST POST /notes/bulk.", s.bulkNotes)
	addTool(s, srv, id, "import_deck", "Import a deck package into a new deck or an existing one (target, dry_run, conflict policy). Give exactly one source: the JSON document returned by export_deck, a base64-encoded .edeck archive, or a public HTTPS direct link (url) to an .edeck/.zip file. For a large package, or one that is already a local file, use create_import_upload instead.", s.importDeck)
	addTool(s, srv, id, "create_import_upload", "Get a single-use URL for importing a deck package whose bytes should not pass through the model. Send the .edeck archive or export_deck's JSON document as the request body with the returned method before expires_at, e.g. curl -sS -T deck.edeck '<upload_url>'; the response is the import report. The import options are fixed when the URL is issued, and the URL works once: request a new one to retry.", s.createImportUpload)
	addTool(s, srv, id, "get_due_cards", "Return cards due for review, including their source fields.", s.getDueCards)
	addTool(s, srv, id, "submit_review", "Submit a review for a card with optimistic version check: a self-assessed rating (1..4), or for card types graded by the server an answer (or give_up) that the server grades.", s.submitReview)

	return srv
}

// filterTools 在 tools/list 结果上按“本次请求”的身份过滤工具；每次请求独立，不会泄漏越权工具。
func (s *Server) filterTools(handshake Identity) sdkmcp.Middleware {
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
			id := s.requestIdentity(handshake, req)
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

// addTool 注册一个工具：调用前必须按本次请求的身份复查该工具所需的 scope。
// 缺 scope 时返回 tool error（置 IsError），而不是把工具从注册表里摘掉 —— 按名字硬调也能得到权限错误。
func addTool[In, Out any](s *Server, srv *sdkmcp.Server, handshake Identity, name, desc string, fn func(context.Context, Identity, In) (Out, error)) {
	scope := toolScopes[name]
	tool := &sdkmcp.Tool{Name: name, Description: desc}
	sdkmcp.AddTool[In, Out](srv, tool, func(ctx context.Context, req *sdkmcp.CallToolRequest, in In) (*sdkmcp.CallToolResult, Out, error) {
		var zero Out
		id := s.requestIdentity(handshake, req)
		if !hasScope(id, scope) {
			// 与 REST 共用同一 code 与英文文案；scope 名作为细节附后。
			return nil, zero, fmt.Errorf("%s: %s — %s", api.CodeScopeRequired, api.ErrorMessage(ctx, api.CodeScopeRequired), scope)
		}
		out, err := fn(ctx, id, in)
		if err != nil {
			// MCP 没有 HTTP 错误包壳：用稳定 code + 英文文案渲染，与 REST 保持一致。
			return nil, zero, errors.New(api.ErrorText(ctx, err))
		}
		return nil, out, nil
	})
}
