// Package mcp provides the built-in MCP server: in-process HTTP (POST /mcp, streamable HTTP),
// reusing user-level API key authentication and the internal/api service layer.
//
// It offers HTTP only, no stdio (a multi-user service has no "process equals identity" semantics).
// The tools do not wrap business logic; they only validate arguments and call exactly the same methods as REST.
//
// /mcp accepts the API key channel only: a session cookie is a browser channel with different revocation and
// scope semantics from the machine interface, so requests without a key are always refused. Every request recomputes identity from the key it carries.
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

// version is injected at build time; dev when not injected, used only for MCP implementation info.
var version = "dev"

// Deps are the MCP server's explicit dependencies: it reuses the already assembled REST API (the same service methods).
type Deps struct {
	API    *api.API
	Logger *slog.Logger
}

// Server holds the streamable HTTP handler; each request recomputes identity and the tool subset from the API key it carries itself.
type Server struct {
	api     *api.API
	logger  *slog.Logger
	handler *sdkmcp.StreamableHTTPHandler

	// mu protects sessions. sessions records the user and the registration moment of each session handshake (initialize),
	// for later requests to verify session ownership (rule 2). The SDK holds the session object itself; this table only records the owner additionally.
	// The table has a capacity cap (mcpSessionCap); when exceeded, the oldest entry by registration time is evicted, so long-running processes do not grow without bound.
	mu       sync.Mutex
	sessions map[string]sessionEntry

	// clock gives the current time; time.Now in production, injectable in tests to get a deterministic registration time.
	clock func() time.Time
}

// sessionEntry is one session record in the owner table: the handshake user + the registration time (seenAt is used for capacity eviction).
type sessionEntry struct {
	userID uint64
	seenAt time.Time
}

// mcpSessionCap is the capacity cap of the owner table. The client registers one entry per session opened, and the entry is cleaned up on a normal close (DELETE 2xx)
// or when the SDK has already forgotten the session (404); the cap is a backstop, so a client that never sends DELETE cannot grow the table without bound.
// It uses "evict the oldest seenAt" rather than "discard the whole batch": a new session is more likely still in use than an old one, so evicting
// one at a time by time always keeps the most recently active batch of sessions.
const mcpSessionCap = 4096

// New builds the MCP server; API is required (all business logic reuses api's service layer).
func New(deps Deps) (*Server, error) {
	if deps.API == nil {
		return nil, errors.New("mcp: Deps.API is required")
	}
	logger := deps.Logger
	if logger == nil {
		logger = slog.Default()
	}
	s := &Server{api: deps.API, logger: logger, sessions: map[string]sessionEntry{}, clock: time.Now}
	// getServer is called only once, when a session is established: it takes identity from the handshake request's context and builds
	// the tool set for the scope in force at that time. Later requests in the session reuse this Server, so the identity in its closure serves only as owner and user source;
	// tool visibility and invocation permission are recomputed per request from that request's key (see requestIdentity).
	s.handler = sdkmcp.NewStreamableHTTPHandler(func(r *http.Request) *sdkmcp.Server {
		id, _ := IdentityFrom(r.Context())
		return s.build(id)
	}, &sdkmcp.StreamableHTTPOptions{})
	return s, nil
}

// mcpSessionHeader is the request/response header carrying the session number in the streamable HTTP spec.
const mcpSessionHeader = "Mcp-Session-Id"

// The next two headers are this project's internal convention: ServeHTTP writes them on each request from the current key, and handlers use them to recompute identity.
// The SDK passes the current HTTP request's headers to the handler with the request (RequestExtra.Header), which is the channel for getting "the key this
// request actually carries" to the handler. ServeHTTP always overwrites headers of the same name, so a client cannot forge them.
const (
	requestKeyIDHeader  = "X-Engram-Mcp-Api-Key-Id"
	requestScopesHeader = "X-Engram-Mcp-Scopes"
)

// ServeHTTP implements http.Handler; the caller must first complete authentication and put the identity into the request context
// (see WithIdentity), reusing the same API key validation, rate limiting and auditing as REST.
//
// It additionally enforces two rules:
//  1. /mcp accepts API keys only: an identity without a key (the session cookie channel) is always 401.
//  2. Within a session, every request's key owner must equal the handshake user: the session number only proves "the same client",
//     not "the same user", otherwise B could use their own key with A's session number to borrow A's identity.
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
	// Rule 3: carry the identity of this request's key. Tool visibility and permissions are computed from this request's key, not from the
	// copy fixed at handshake time — so switching to a lower-scope key within a high-scope session of the same user cannot escalate privileges.
	r.Header.Set(requestKeyIDHeader, strconv.FormatUint(id.KeyID, 10))
	r.Header.Set(requestScopesHeader, id.Scopes)

	rec := &statusWriter{ResponseWriter: w}
	s.handler.ServeHTTP(rec, r)

	switch {
	case sid == "":
		// New session: the successful handshake response carries Mcp-Session-Id; record its owner for later requests to verify.
		if newSID := rec.Header().Get(mcpSessionHeader); newSID != "" {
			s.rememberSession(newSID, id.User.ID)
		}
	case r.Method == http.MethodDelete && rec.status >= http.StatusOK && rec.status < http.StatusMultipleChoices:
		// Rule 4: when the client closes the session normally with DELETE /mcp (2xx), the owner record must be cleared as well.
		// Miss this path and session entries would only be cleared on 404, growing without bound in a long-running process (F10c).
		s.forgetSession(sid)
	case rec.status == http.StatusNotFound:
		// The session no longer exists (expired or already DELETEd): clear the owner record while we are at it, so this table does not grow without bound.
		s.forgetSession(sid)
	}
}

// sessionOwnerMismatch reports whether the session is registered and its owner is not userID. An unregistered session returns false,
// leaving it to the SDK's existing "no such session" behavior.
func (s *Server) sessionOwnerMismatch(sid string, userID uint64) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	entry, known := s.sessions[sid]
	return known && entry.userID != userID
}

// now returns the current instant; falls back to time.Now when clock was not injected (normal assembly injects a real clock via New).
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

// evictOldestLocked deletes n entries from oldest to newest by seenAt; the caller must already hold s.mu.
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

// statusWriter records the response status code, for ServeHTTP to tell whether the handshake succeeded and whether the session has disappeared.
// Unwrap lets http.NewResponseController(w).Flush() still reach the real ResponseWriter,
// so the SSE streaming response is not broken.
type statusWriter struct {
	http.ResponseWriter
	status int
}

func (w *statusWriter) WriteHeader(code int) {
	w.status = code
	w.ResponseWriter.WriteHeader(code)
}

func (w *statusWriter) Unwrap() http.ResponseWriter { return w.ResponseWriter }

// ---- identity context ----

type identityKey struct{}

// Identity is the authenticated identity of one MCP request: the user and the authorization info of the API key that request carries.
// MCP accepts the API key channel only (see ServeHTTP rule 1), so only a non-zero KeyID is a usable identity.
type Identity struct {
	User   *store.User
	KeyID  uint64 // Primary key of the API key this request carries; 0 means no key
	Scopes string // The normalized scopes string of that key
}

// WithIdentity injects the authenticated identity into the request context; the web assembly layer calls it after the auth middleware.
// A nil k means the session cookie channel (so there is no key identity, and MCP will refuse).
func WithIdentity(ctx context.Context, u *store.User, k *store.APIKey) context.Context {
	id := Identity{User: u}
	if k != nil {
		id.KeyID, id.Scopes = k.ID, k.Scopes
	}
	return context.WithValue(ctx, identityKey{}, id)
}

// IdentityFrom retrieves the identity; ok=false when missing.
func IdentityFrom(ctx context.Context) (Identity, bool) {
	id, ok := ctx.Value(identityKey{}).(Identity)
	return id, ok && id.User != nil
}

// requestIdentity returns the identity actually in force for this request.
//
// The SDK builds a Server only once when the session is established, and every later request in the same session reuses that Server and the handshake identity
// in its closure. So a tool handler must not judge scope from the handshake identity; it must read the key carried by this request
// (see the headers ServeHTTP writes). The key's owner has already been confirmed by ServeHTTP to match the handshake user, so the user
// is taken straight from the handshake copy. When the header is missing or malformed it falls back to the handshake identity.
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

// apiKeyID returns the id of this request's key; nil when there is no key.
func (id Identity) apiKeyID() *uint64 {
	if id.KeyID == 0 {
		return nil
	}
	return store.Ptr(id.KeyID)
}

// hasScope reports whether the identity covers scope: MCP has only the key channel, so it judges by the key's scopes
// (admin implies the other three tiers).
func hasScope(id Identity, scope string) bool {
	if id.KeyID == 0 {
		return false
	}
	return store.HasScope(id.Scopes, scope)
}

// toolScopes maps tool names to the scope they require; shared by handshake filtering and invocation re-checks, so both use the same rules.
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
	"delete_deck":          store.ScopeWrite,
	"bulk_decks":           store.ScopeWrite,
	"create_notes":         store.ScopeWrite,
	"update_note":          store.ScopeWrite,
	"delete_note":          store.ScopeWrite,
	"bulk_notes":           store.ScopeWrite,
	"import_deck":          store.ScopeWrite,
	"create_import_upload": store.ScopeWrite,
	"get_due_cards":        store.ScopeReview,
	"submit_review":        store.ScopeReview,
	"add_extra_new_cards":  store.ScopeReview,
	"set_deck_paused":      store.ScopeReview,
	"bury_card":            store.ScopeReview,
}

const instructions = "Engram library and scheduler. Tools mirror the REST /api/v1 surface " +
	"and are filtered by the API key's scopes (read/write/review). " +
	"Every tool argument is generated by the model, so do not pass a large deck package or a package " +
	"that already exists as a file through import_deck: call create_import_upload and send the file " +
	"to the returned upload_url with an HTTP client such as curl."

// build builds the tool set for one request: it registers every tool (so "calling by name" returns a permission error),
// and filters tools/list by identity, so the list a client sees is exactly the set it can really use.
func (s *Server) build(id Identity) *sdkmcp.Server {
	srv := sdkmcp.NewServer(&sdkmcp.Implementation{Name: "engram", Version: version}, &sdkmcp.ServerOptions{
		Instructions: instructions,
	})
	srv.AddReceivingMiddleware(s.filterTools(id))

	addTool(s, srv, id, "list_decks", "List the decks visible to the caller (owned, or granted by another user).", s.listDecks)
	addTool(s, srv, id, "create_deck", "Create an empty deck (name required; preset_id 0 uses the caller's Default preset).", s.createDeck)
	addTool(s, srv, id, "update_deck", "Update a deck's name and description (owner only).", s.updateDeck)
	addTool(s, srv, id, "delete_deck", "Delete a deck with all of its notes, cards and progress (owner only; irreversible).", s.deleteDeck)
	addTool(s, srv, id, "bulk_decks", "Apply one bulk action (delete) to a set of decks; owner-only per row, dry_run counts without writing. Mirrors REST POST /decks/bulk.", s.bulkDecks)
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
	addTool(s, srv, id, "get_due_cards", "Return cards due for review, including their source fields. A choice card's option_order gives the display order of its options: display slot b shows stored option index option_order[b], and a submitted answer still takes a stored option index.", s.getDueCards)
	addTool(s, srv, id, "submit_review", "Submit a review for a card with optimistic version check: a self-assessed rating (1..4), or for card types graded by the server an answer (or give_up) that the server grades.", s.submitReview)
	addTool(s, srv, id, "add_extra_new_cards", "Allow extra new cards in one deck for the caller's current review day only, after the daily new card limit is reached. The deck settings do not change, the next review day returns to the daily limit, and other members of a shared deck are not affected. Decks without a daily new card limit return invalid_request. get_due_cards then includes the extra new cards.", s.addExtraNewCards)
	addTool(s, srv, id, "bury_card", "Postpone one card to the start of the caller's next review day: it leaves today's get_due_cards without any change to its progress and writes no review. The next day is computed in the caller's timezone and day cutoff. Only the caller's own progress is affected, so a reader of a shared deck may bury cards. Mirrors REST POST /cards/{id}/bury.", s.buryCard)
	addTool(s, srv, id, "set_deck_paused", "Pause or resume one deck for the caller only. A paused deck stays in list_decks with paused=true but leaves get_due_cards when no deck is given, the due count of get_stats and the reminder mail; naming the deck in get_due_cards still returns its cards. Other members of a shared deck are not affected, progress is kept, and cards keep falling due while the deck is paused. Pausing twice or resuming twice is not an error.", s.setDeckPaused)

	return srv
}

// filterTools filters tools on the tools/list result by "this request's" identity; each request is independent, so a tool the caller may not use never leaks.
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

// addTool registers a tool: before invocation it must re-check the scope the tool requires against this request's identity.
// When the scope is missing it returns a tool error (IsError set) rather than removing the tool from the registry — calling by name still gets a permission error.
func addTool[In, Out any](s *Server, srv *sdkmcp.Server, handshake Identity, name, desc string, fn func(context.Context, Identity, In) (Out, error)) {
	scope := toolScopes[name]
	tool := &sdkmcp.Tool{Name: name, Description: desc}
	sdkmcp.AddTool[In, Out](srv, tool, func(ctx context.Context, req *sdkmcp.CallToolRequest, in In) (*sdkmcp.CallToolResult, Out, error) {
		var zero Out
		id := s.requestIdentity(handshake, req)
		if !hasScope(id, scope) {
			// Shares the same code and English copy as REST; the scope name is appended as detail.
			return nil, zero, fmt.Errorf("%s: %s — %s", api.CodeScopeRequired, api.ErrorMessage(ctx, api.CodeScopeRequired), scope)
		}
		out, err := fn(ctx, id, in)
		if err != nil {
			// MCP has no HTTP error envelope: render a stable code + English copy, consistent with REST.
			return nil, zero, errors.New(api.ErrorText(ctx, err))
		}
		return nil, out, nil
	})
}
