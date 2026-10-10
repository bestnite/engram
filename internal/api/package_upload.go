package api

import (
	"bufio"
	"bytes"
	"context"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"time"

	"github.com/gin-gonic/gin"

	"git.nite07.com/nite/engram/internal/media"
	"git.nite07.com/nite/engram/internal/store"
)

// 凭一次性票据上传卡组包：
//   POST /api/v1/decks/import-uploads         签发票据（write）
//   PUT  /api/v1/decks/import-uploads/:token  凭票据上传并导入（票据即凭据）
//
// 为什么需要它：MCP 工具的每个参数都由模型逐 token 输出，大卡组经 import_deck 的 package
// 参数传入，等于让模型把整个包再吐一遍；而 agent 往往拿不到 API key（key 配在 MCP 客户端
// 的请求头里，模型看不见），也就无法直接调 REST 上传。票据把两件事拆开：鉴权在签发时
// 完成（MCP 或 REST 都可签发），字节由 agent 用 curl 之类的工具直传，不经过模型，key 也
// 不离开客户端。
//
// 票据存在 action_tokens 表：库里只有 sha256 摘要，消费走条件更新保证一次性，过期行由
// internal/retention 统一清理。导入选项在签发时固定进票据，上传端不再接受任何选项，
// 因此持有 URL 的人只能做签发者已经授权的那一次导入。

const (
	// importUploadPurpose 是上传票据在 action_tokens 里的用途值。
	importUploadPurpose = "deck_import_upload"
	// importUploadTTL 是票据有效期：够 agent 打包并上传一个大包，又让泄漏的 URL 很快作废。
	importUploadTTL = 15 * time.Minute
	// importUploadPathPrefix 是上传端点的路径前缀，票据明文紧随其后。
	importUploadPathPrefix = "/api/v1/decks/import-uploads/"
	// importUploadCreateBodyLimit 是签发请求体的上限：只有一个短 JSON。
	importUploadCreateBodyLimit = 64 << 10
)

// ImportUpload 是签发结果：agent 用 Method 把包字节发到 UploadURL。
type ImportUpload struct {
	UploadURL string    `json:"upload_url"`
	Method    string    `json:"method"`
	ExpiresAt time.Time `json:"expires_at"`
	// MaxBytes 是上传体的字节上限（与文件导入同一配置项），让调用方上传前就能自查。
	MaxBytes int64 `json:"max_bytes"`
}

// importUploadTicket 是写进 action_tokens.payload 的票据内容：签发时固定的导入选项，
// 以及签发所用的 API key（上传时复查它仍然有效）。
type importUploadTicket struct {
	Target              string  `json:"target"`
	DryRun              bool    `json:"dry_run"`
	OnConflict          string  `json:"on_conflict"`
	SkipMissingMedia    bool    `json:"skip_missing_media"`
	AllowOthersProgress bool    `json:"allow_others_progress"`
	ApplyWeights        bool    `json:"apply_weights"`
	APIKeyID            *uint64 `json:"api_key_id,omitempty"`
}

// importUploadRequest 是 POST /api/v1/decks/import-uploads 的请求体，字段与文件导入的选项一一对应。
type importUploadRequest struct {
	Target              string `json:"target"`
	DryRun              bool   `json:"dry_run"`
	OnConflict          string `json:"on_conflict"`
	AllowOthersProgress bool   `json:"allow_others_progress"`
	SkipMissingMedia    bool   `json:"skip_missing_media"`
	ApplyWeights        bool   `json:"apply_weights"`
}

// CreateImportUpload 签发一张一次性上传票据；REST 与 MCP 共用。
//
// 目标卡组的权限在签发时就判定：无权的调用者拿不到票据。上传时还会再判一次（导入走
// ImportDeckPackage），因为签发与上传之间角色可能被收回。
func (a *API) CreateImportUpload(ctx context.Context, u *store.User, apiKeyID *uint64, opts store.PackageImportOptions) (*ImportUpload, error) {
	if _, err := a.authorizeImportTarget(ctx, u.ID, opts.Target); err != nil {
		return nil, err
	}
	// 冲突策略在签发时就校验：错误的选项应当在 agent 上传大包之前暴露，而不是上传之后。
	switch opts.OnConflict {
	case "", "skip", "update", "fail":
	default:
		return nil, InvalidRequest("on_conflict must be one of skip, update, fail")
	}
	payload, err := json.Marshal(importUploadTicket{
		Target:              opts.Target,
		DryRun:              opts.DryRun,
		OnConflict:          opts.OnConflict,
		SkipMissingMedia:    opts.SkipMissingMedia,
		AllowOthersProgress: opts.AllowOthersProgress && u.Role == store.RoleAdmin,
		ApplyWeights:        opts.ApplyWeights,
		APIKeyID:            apiKeyID,
	})
	if err != nil {
		return nil, fmt.Errorf("encode import upload ticket: %w", err)
	}
	buf := make([]byte, 32)
	if _, err := rand.Read(buf); err != nil {
		return nil, fmt.Errorf("generate import upload token: %w", err)
	}
	plaintext := base64.RawURLEncoding.EncodeToString(buf)
	now := a.now().UTC()
	row := &store.ActionToken{
		Purpose:   importUploadPurpose,
		UserID:    u.ID,
		TokenHash: store.HashActionToken(plaintext),
		Payload:   string(payload),
		CreatedAt: now,
		ExpiresAt: now.Add(importUploadTTL),
	}
	if err := a.tokens.Create(ctx, row); err != nil {
		return nil, err
	}
	return &ImportUpload{
		UploadURL: a.baseURL + importUploadPathPrefix + plaintext,
		Method:    http.MethodPut,
		ExpiresAt: row.ExpiresAt,
		MaxBytes:  media.ResolveMaxBytes(ctx, a.db),
	}, nil
}

// ImportDeckPackageUpload 消费票据并导入请求体里的卡组包。
//
// 票据先消费、后读字节：无效票据的请求一个字节都不读；票据一经出示即作废，导入失败也
// 不能重放，调用方重新签发即可（签发很便宜，上传的字节本来就不经过模型）。
// 请求体可以是 .edeck zip，也可以是 export_deck 输出的 JSON 文档，按首个非空白字节区分。
func (a *API) ImportDeckPackageUpload(ctx context.Context, token string, body io.Reader) (*store.PackageImportReport, error) {
	invalid := newServiceError(http.StatusNotFound, CodeImportUploadInvalid, "")
	now := a.now()
	tok, err := a.tokens.Consume(ctx, importUploadPurpose, token, now)
	if err != nil {
		if errors.Is(err, store.ErrActionTokenNotFound) || errors.Is(err, store.ErrActionTokenUsed) || errors.Is(err, store.ErrActionTokenExpired) {
			return nil, invalid
		}
		return nil, err
	}
	var ticket importUploadTicket
	if err := json.Unmarshal([]byte(tok.Payload), &ticket); err != nil {
		return nil, fmt.Errorf("decode import upload ticket: %w", err)
	}
	// 签发后账号被禁用、或签发所用的 key 被撤销 / 过期，票据随之失效：权限边界与 key 本身一致。
	u, err := a.users.ByID(ctx, tok.UserID)
	if err != nil || u.Status != store.StatusActive {
		return nil, invalid
	}
	if ticket.APIKeyID != nil {
		k, err := a.keys.ByID(ctx, *ticket.APIKeyID)
		if err != nil || k.UserID != u.ID || store.ValidateAPIKey(k, now) != nil {
			return nil, invalid
		}
	}
	opts := store.PackageImportOptions{
		Target:              ticket.Target,
		DryRun:              ticket.DryRun,
		OnConflict:          ticket.OnConflict,
		SkipMissingMedia:    ticket.SkipMissingMedia,
		AllowOthersProgress: ticket.AllowOthersProgress,
		ApplyWeights:        ticket.ApplyWeights,
	}
	// 读字节之前先判权，与直链导入同一不变式：被拒的请求不消耗读取与解析。
	if _, err := a.authorizeImportTarget(ctx, u.ID, opts.Target); err != nil {
		return nil, err
	}
	limited := &overflowReader{r: body, remaining: media.ResolveMaxBytes(ctx, a.db)}
	r, err := uploadPackageReader(limited)
	if err != nil {
		err = mapPackageError(err)
	} else {
		var report *store.PackageImportReport
		report, err = a.ImportDeckPackage(ctx, u, ticket.APIKeyID, r, opts)
		if err == nil {
			return report, nil
		}
	}
	// 超限优先于其他错误：截断的字节会让解析报格式错误，那不是真正的原因。
	// code 沿用文件导入的 package_too_large，两处体积上限口径一致。
	if limited.exceeded {
		return nil, newServiceError(http.StatusRequestEntityTooLarge, store.CodePackageTooLarge, "")
	}
	return nil, err
}

// uploadPackageReader 按首个非空白字节判断请求体形态：'{' 是 JSON 文档，交给与 MCP 相同的
// store.PackageReader 还原成 zip；其余原样当作 .edeck 字节。agent 因此不必自己打 zip。
func uploadPackageReader(body io.Reader) (io.Reader, error) {
	br := bufio.NewReader(body)
	head, err := br.Peek(64)
	if err != nil && !errors.Is(err, io.EOF) && !errors.Is(err, bufio.ErrBufferFull) {
		return nil, err
	}
	trimmed := bytes.TrimLeft(head, " \t\r\n")
	if len(trimmed) == 0 || trimmed[0] != '{' {
		return br, nil
	}
	var doc map[string]any
	if err := json.NewDecoder(br).Decode(&doc); err != nil {
		return nil, &store.PackageError{Code: store.CodePackageBadFormat, Message: "package JSON document is malformed"}
	}
	return store.PackageReader(doc)
}

// overflowReader 最多放行 remaining 个字节；越界时记下 exceeded 并返回错误。
// 记录越界是为了在导入失败后区分「包太大被截断」与「包本身格式错」。
type overflowReader struct {
	r         io.Reader
	remaining int64
	exceeded  bool
}

func (o *overflowReader) Read(p []byte) (int, error) {
	if o.remaining <= 0 {
		// 已到上限：再试读一个字节，读到就是越界；读到 EOF 说明包恰好等于上限。
		var one [1]byte
		n, err := o.r.Read(one[:])
		if n > 0 {
			o.exceeded = true
			return 0, errors.New("import upload body exceeds the size limit")
		}
		return 0, err
	}
	if int64(len(p)) > o.remaining {
		p = p[:o.remaining]
	}
	n, err := o.r.Read(p)
	o.remaining -= int64(n)
	return n, err
}

// handleCreateImportUpload 是 POST /api/v1/decks/import-uploads（application/json）。
func (a *API) handleCreateImportUpload(c *gin.Context) {
	u, _ := CurrentUser(c)
	c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, importUploadCreateBodyLimit)
	var req importUploadRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		if isRequestBodyTooLarge(err) {
			abortError(c, http.StatusRequestEntityTooLarge, CodeInvalidRequest, "request body too large")
			return
		}
		abortError(c, http.StatusBadRequest, CodeInvalidRequest, "")
		return
	}
	upload, err := a.CreateImportUpload(c.Request.Context(), u, CurrentAPIKeyID(c), store.PackageImportOptions{
		Target:              req.Target,
		DryRun:              req.DryRun,
		OnConflict:          req.OnConflict,
		AllowOthersProgress: req.AllowOthersProgress,
		SkipMissingMedia:    req.SkipMissingMedia,
		ApplyWeights:        req.ApplyWeights,
	})
	if err != nil {
		writeServiceError(c, err)
		return
	}
	c.JSON(http.StatusCreated, upload)
}

// handleImportUpload 是 PUT /api/v1/decks/import-uploads/:token：请求体即包字节。
//
// 访问日志会记下含票据的路径；记录发生在请求结束时，此时票据已被本请求消费，日志里的
// 明文不能再用于任何导入。
func (a *API) handleImportUpload(c *gin.Context) {
	report, err := a.ImportDeckPackageUpload(c.Request.Context(), c.Param("token"), c.Request.Body)
	if err != nil {
		writeServiceError(c, err)
		return
	}
	c.JSON(http.StatusOK, report)
}
