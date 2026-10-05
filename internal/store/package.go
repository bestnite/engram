package store

import (
	"archive/zip"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"

	"gorm.io/gorm"
)

// 卡组包（.edeck）导出/导入（DESIGN.md §7.6，AGENTS.md M5-6/M5-7）。
//
// 格式：单个 zip，含 manifest.json / notes.json / cards.json / preset.json，
// 可选 progress.json 与 media/ + media.json。它把\"一个卡组\"变成自包含文件，
// 用于备份、跨实例迁移与离线转交；与克隆（同实例复制）语义一致：内容走、进度不跟随。

// PackageFormatVersion 是当前包格式版本；导入端至少兼容 N-1（DESIGN.md §7.6）。
const PackageFormatVersion = 1

// 稳定的导入错误 code（英文标识符，供调用方程序化判断；AGENTS.md §2.1）。
const (
	CodePackageUnsafeEntry = "package_unsafe_entry"
	CodePackageTooLarge    = "package_too_large"
	CodePackageBadFormat   = "package_bad_format"
	// CodePackageUnknownKind 表示包里有本实例不认识（或未注册）的题型；必须报错并逐条列出。
	CodePackageUnknownKind = "package_unknown_kind"
	// CodePackageUnsafeMedia 表示包内媒体的真实字节不在白名单内，或与 media.json 的声明不符；逐条列出。
	CodePackageUnsafeMedia = "package_unsafe_media"
)

// PackageError 是带稳定 code 的卡组包错误。
type PackageError struct {
	Code    string
	Message string
	// Entries 列出出错的包内条目（如未知题型的 note 下标），可空。
	Entries []string
}

func (e *PackageError) Error() string {
	if len(e.Entries) == 0 {
		return e.Code + ": " + e.Message
	}
	return e.Code + ": " + e.Message + ": " + strings.Join(e.Entries, ", ")
}

// PackageOptions 控制一次导出的内容（DESIGN.md §7.6 的三个开关）。
type PackageOptions struct {
	// IncludeProgress 默认 off：卡组包主要用途是把内容给别人/搬到别的实例（§13 #8）。
	IncludeProgress bool
	// IncludeMedia 默认 on。
	IncludeMedia bool
	// IncludeReviews 依赖 IncludeProgress；仅当两者都为真才写出复习日志。
	IncludeReviews bool
	// MediaRoot 是媒体字节的本地根目录（media.Store.Root()）；为空表示无法内联媒体。
	MediaRoot string
	// AppVersion 写入 manifest.app_version，仅作提示。
	AppVersion string
	// Now 可注入时钟；零值时用系统 UTC 时间。
	Now func() time.Time
}

// PackageManifest 是包内 manifest.json（字段名已冻结，DESIGN.md §7.6）。
type PackageManifest struct {
	FormatVersion   int          `json:"format_version"`
	ExportedAt      string       `json:"exported_at"`
	AppVersion      string       `json:"app_version,omitempty"`
	Deck            PackageDeck  `json:"deck"`
	IncludeProgress bool         `json:"include_progress"`
	IncludeMedia    bool         `json:"include_media"`
	IncludeReviews  bool         `json:"include_reviews"`
	Counts          PackageCount `json:"counts"`
	// ExportedBy 是导出者的不透明标识（用登录名，不是数据库 id），
	// 导入端据此判断包内进度是否属于导入者本人（DESIGN.md §7.6「进度归属判定」）。
	ExportedBy string `json:"exported_by,omitempty"`
}

// PackageDeck 是随包走的卡组元信息；授权、可见性、审计等实例内状态一概不导出。
type PackageDeck struct {
	Name        string `json:"name"`
	Description string `json:"description,omitempty"`
}

// PackageCount 是 manifest 的条目计数。
type PackageCount struct {
	Notes int `json:"notes"`
	Cards int `json:"cards"`
	Media int `json:"media,omitempty"`
}

// PackageNote 是 notes.json 的一条；形状与 §7.3 的导入体一致。
type PackageNote struct {
	Kind        string         `json:"kind"`
	Fields      map[string]any `json:"fields"`
	ExternalRef string         `json:"external_ref,omitempty"`
	Tags        []string       `json:"tags,omitempty"`
	Extra       map[string]any `json:"extra,omitempty"`
	SourceURL   string         `json:"source_url,omitempty"`
}

// PackageCard 用 note_index（指向 notes[] 的下标）而非 ID，避免导出/导入之间的 ID 语义纠缠。
type PackageCard struct {
	NoteIndex int    `json:"note_index"`
	Template  string `json:"template"`
	Ordinal   int    `json:"ordinal"`
}

// PackagePreset 是随包走的调度参数；weights 为 21 个数字或 null（null = 用默认权重）。
type PackagePreset struct {
	Name                string    `json:"name,omitempty"`
	DesiredRetention    float64   `json:"desired_retention"`
	LearningSteps       string    `json:"learning_steps"`
	RelearningSteps     string    `json:"relearning_steps"`
	MaximumIntervalDays int       `json:"maximum_interval_days"`
	EnableFuzz          bool      `json:"enable_fuzz"`
	Weights             []float64 `json:"weights"`
	WeightsOptimizedAt  *string   `json:"weights_optimized_at"`
	WeightsReviewCount  *int      `json:"weights_review_count"`
}

// PackageCardState 是 progress.json 里的一条进度；用 note_index 或 external_ref 定位 note。
type PackageCardState struct {
	NoteIndex     *int     `json:"note_index,omitempty"`
	ExternalRef   string   `json:"external_ref,omitempty"`
	Template      string   `json:"template,omitempty"`
	State         string   `json:"state"`
	DueAt         *string  `json:"due_at"`
	StepIndex     int      `json:"step_index"`
	Stability     *float64 `json:"stability"`
	Difficulty    *float64 `json:"difficulty"`
	Reps          int      `json:"reps"`
	Lapses        int      `json:"lapses"`
	ScheduledDays int      `json:"scheduled_days"`
	ElapsedDays   int      `json:"elapsed_days"`
	LastReviewAt  *string  `json:"last_review_at"`
	Version       int      `json:"version"`
}

// PackageProgress 只含导出者本人的进度；导入他人包时默认丢弃。
type PackageProgress struct {
	CardStates []PackageCardState `json:"card_states"`
	Reviews    []PackageReview    `json:"reviews,omitempty"`
}

// PackageReview 是 progress.json 里可选的一条复习日志。
type PackageReview struct {
	NoteIndex    *int     `json:"note_index,omitempty"`
	ExternalRef  string   `json:"external_ref,omitempty"`
	Template     string   `json:"template,omitempty"`
	Rating       int      `json:"rating"`
	GradeSource  string   `json:"grade_source,omitempty"`
	ReviewedAt   string   `json:"reviewed_at"`
	ReviewDay    string   `json:"review_day,omitempty"`
	ElapsedMS    *int     `json:"elapsed_ms"`
	DurationDays *float64 `json:"duration_days"`
	StateBefore  int      `json:"state_before"`
	IntervalDays *float64 `json:"interval_days"`
	Stability    *float64 `json:"stability"`
	Difficulty   *float64 `json:"difficulty"`
}

// PackageMediaEntry 是 media.json 的一条：sha256 → 包内相对路径 + mime。
type PackageMediaEntry struct {
	Path   string `json:"path"`
	Mime   string `json:"mime"`
	Bytes  int64  `json:"bytes,omitempty"`
	Width  *int   `json:"width,omitempty"`
	Height *int   `json:"height,omitempty"`
}

// DeckPackage 是一个已装配好的卡组包（内存形态）。媒体字节按 sha256 索引，
// 便于导出时写入 zip、或把整包渲染成一个 JSON 文档做 schema 校验。
type DeckPackage struct {
	Manifest PackageManifest
	Notes    []PackageNote
	Cards    []PackageCard
	Preset   PackagePreset
	Progress *PackageProgress
	// Media 是 sha256 → 条目；MediaBytes 是同一 sha256 → 字节。
	Media      map[string]PackageMediaEntry
	MediaBytes map[string][]byte
}

var (
	// ErrPackageDeckRequired 表示导出未指定卡组。
	ErrPackageDeckRequired = errors.New("deck package: deck id is required")
	// ErrPackageProgressRequiresReviews 表示只要求复习日志却没有开进度。
	ErrPackageProgressRequiresReviews = errors.New("deck package: include_reviews requires include_progress")
	// ErrPackageMediaMissing 表示包内引用了媒体但字节不可得。
	ErrPackageMediaMissing = errors.New("deck package: media bytes are unavailable")
)

// mediaRefRE 匹配 note 字段里形如 media/<sha256>.<ext> 的媒体引用（DESIGN.md §7.6 示例）。
var mediaRefRE = regexp.MustCompile(`^media/([a-f0-9]{64})\.([A-Za-z0-9]+)$`)

// ExportPackage 装配一个卡组包（DESIGN.md §7.6、M5-6）。
//
// 边界（内容与进度分离）：
//   - 进度只取 actorUserID 本人的 card_states；导出他人共享的卡组时，包里绝不含他人进度。
//   - 授权、可见性、审计等实例内状态一概不导出。
//   - include_media=0 时 Media 为空、manifest.include_media=false，如实声明。
func (s *DeckStore) ExportPackage(ctx context.Context, actorUserID, deckID uint64, opts PackageOptions) (*DeckPackage, error) {
	if deckID == 0 {
		return nil, ErrPackageDeckRequired
	}
	if opts.IncludeReviews && !opts.IncludeProgress {
		return nil, ErrPackageProgressRequiresReviews
	}
	if opts.Now == nil {
		opts.Now = func() time.Time { return time.Now().UTC() }
	}
	deck, err := s.ByID(ctx, deckID)
	if err != nil {
		return nil, fmt.Errorf("export package: load deck: %w", err)
	}
	preset, err := NewPresetStore(s.db).ByID(ctx, deck.PresetID)
	if err != nil {
		return nil, fmt.Errorf("export package: load preset: %w", err)
	}

	// 只导出未软删除的 note（GORM 默认作用域）；cards 一并按 note 收集。
	var notes []Note
	if err := s.db.WithContext(ctx).Where("deck_id = ?", deckID).Order("id ASC").Find(&notes).Error; err != nil {
		return nil, fmt.Errorf("export package: list notes: %w", err)
	}

	pkg := &DeckPackage{
		Preset: presetToPackage(preset),
		Media:  map[string]PackageMediaEntry{},
	}
	if !opts.IncludeMedia {
		pkg.MediaBytes = nil
	} else {
		pkg.MediaBytes = map[string][]byte{}
	}

	// note_index → note id，供进度映射使用。
	noteIDByIndex := make([]uint64, 0, len(notes))
	refs := map[string]bool{} // 引用到的媒体 sha256 → 首次出现
	for i := range notes {
		n := &notes[i]
		fields, err := ParseFields(n.FieldsJSON)
		if err != nil {
			return nil, fmt.Errorf("export package: note %d: %w", n.ID, err)
		}
		tags, err := ParseTags(n.TagsJSON)
		if err != nil {
			return nil, fmt.Errorf("export package: note %d: %w", n.ID, err)
		}
		pn := PackageNote{Kind: n.Kind, Fields: fields, Tags: tags}
		if n.ExternalRef != nil {
			pn.ExternalRef = *n.ExternalRef
		}
		pkg.Notes = append(pkg.Notes, pn)
		noteIDByIndex = append(noteIDByIndex, n.ID)

		var cards []Card
		if err := s.db.WithContext(ctx).Where("note_id = ?", n.ID).
			Order("ordinal ASC, id ASC").Find(&cards).Error; err != nil {
			return nil, fmt.Errorf("export package: list cards for note %d: %w", n.ID, err)
		}
		for _, c := range cards {
			pkg.Cards = append(pkg.Cards, PackageCard{NoteIndex: i, Template: c.Template, Ordinal: c.Ordinal})
		}
		if opts.IncludeMedia {
			collectMediaRefs(fields, refs)
		}
	}

	// 媒体：按 sha256 去重后内联；字节来自本地媒体根目录。
	if opts.IncludeMedia && len(refs) > 0 {
		mstore := NewMediaStore(s.db)
		for sha := range refs {
			m, err := mstore.BySha256(ctx, sha)
			if err != nil {
				return nil, fmt.Errorf("export package: media metadata %s: %w", sha, err)
			}
			if m == nil {
				return nil, fmt.Errorf("%w: sha256=%s", ErrPackageMediaMissing, sha)
			}
			ext := strings.TrimPrefix(filepath.Ext(m.RelPath), ".")
			rel := "media/" + sha + "." + ext
			raw, err := os.ReadFile(filepath.Join(opts.MediaRoot, filepath.FromSlash(m.RelPath)))
			if err != nil {
				return nil, fmt.Errorf("%w: sha256=%s: %v", ErrPackageMediaMissing, sha, err)
			}
			pkg.Media[sha] = PackageMediaEntry{Path: rel, Mime: m.Mime, Bytes: m.Bytes, Width: m.Width, Height: m.Height}
			pkg.MediaBytes[sha] = raw
		}
	}

	pkg.Manifest = PackageManifest{
		FormatVersion:   PackageFormatVersion,
		ExportedAt:      opts.Now().UTC().Format(time.RFC3339),
		AppVersion:      opts.AppVersion,
		Deck:            PackageDeck{Name: deck.Name, Description: deck.Description},
		IncludeProgress: opts.IncludeProgress,
		IncludeMedia:    opts.IncludeMedia,
		IncludeReviews:  opts.IncludeReviews,
		Counts:          PackageCount{Notes: len(pkg.Notes), Cards: len(pkg.Cards), Media: len(pkg.Media)},
	}

	if opts.IncludeProgress {
		prog, err := s.exportProgress(ctx, actorUserID, noteIDByIndex, opts.IncludeReviews)
		if err != nil {
			return nil, err
		}
		pkg.Progress = prog
		if u, err := s.lookupUsername(ctx, actorUserID); err == nil {
			pkg.Manifest.ExportedBy = u
		}
	}
	return pkg, nil
}

// exportProgress 只导出 actorUserID 本人的 card_states（+ 可选 reviews）。
// 这是"绝不包含他人进度"的实现点：查询条件写死 user_id = actorUserID。
func (s *DeckStore) exportProgress(ctx context.Context, actorUserID uint64, noteIDs []uint64, withReviews bool) (*PackageProgress, error) {
	indexByNoteID := make(map[uint64]int, len(noteIDs))
	for i, id := range noteIDs {
		indexByNoteID[id] = i
	}
	prog := &PackageProgress{CardStates: []PackageCardState{}}

	// 通过 cards join 限定在本卡组的 note 上。
	type row struct {
		CardState
		NoteID   uint64
		Template string
	}
	var rows []row
	if err := s.db.WithContext(ctx).Table("card_states").
		Select("card_states.*, cards.note_id AS note_id, cards.template AS template").
		Joins("JOIN cards ON cards.id = card_states.card_id AND cards.deleted_at IS NULL").
		Where("card_states.user_id = ? AND cards.note_id IN ?", actorUserID, noteIDs).
		Order("card_states.card_id ASC").Scan(&rows).Error; err != nil {
		return nil, fmt.Errorf("export package: list progress: %w", err)
	}
	cardIDs := make([]uint64, 0, len(rows))
	stateByCardID := make(map[uint64]PackageCardState, len(rows))
	for _, r := range rows {
		idx := indexByNoteID[r.NoteID]
		ps := cardStateToPackage(r.CardState, &idx, r.Template)
		prog.CardStates = append(prog.CardStates, ps)
		cardIDs = append(cardIDs, r.CardID)
		stateByCardID[r.CardID] = ps
	}

	if withReviews && len(cardIDs) > 0 {
		var reviews []Review
		if err := s.db.WithContext(ctx).Where("user_id = ? AND card_id IN ?", actorUserID, cardIDs).
			Order("id ASC").Find(&reviews).Error; err != nil {
			return nil, fmt.Errorf("export package: list reviews: %w", err)
		}
		// card_id → note_index/template（复用上面的状态映射）。
		cardIndex := map[uint64]int{}
		cardTemplate := map[uint64]string{}
		for _, r := range rows {
			cardIndex[r.CardID] = indexByNoteID[r.NoteID]
			cardTemplate[r.CardID] = r.Template
		}
		prog.Reviews = make([]PackageReview, 0, len(reviews))
		for i := range reviews {
			rv := reviews[i]
			idx := cardIndex[rv.CardID]
			prog.Reviews = append(prog.Reviews, PackageReview{
				NoteIndex:    &idx,
				Template:     cardTemplate[rv.CardID],
				Rating:       rv.Rating,
				GradeSource:  rv.GradeSource,
				ReviewedAt:   rv.ReviewedAt.UTC().Format(time.RFC3339),
				ReviewDay:    rv.ReviewDay,
				ElapsedMS:    rv.ElapsedMS,
				DurationDays: rv.DurationDays,
				StateBefore:  rv.StateBefore,
				IntervalDays: rv.IntervalDays,
				Stability:    rv.Stability,
				Difficulty:   rv.Difficulty,
			})
		}
	}
	return prog, nil
}

func (s *DeckStore) lookupUsername(ctx context.Context, userID uint64) (string, error) {
	var u User
	if err := s.db.WithContext(ctx).Select("id", "username").First(&u, "id = ?", userID).Error; err != nil {
		return "", err
	}
	return u.Username, nil
}

// WriteZip 把包写成 .edeck zip；entries 顺序固定，便于 diff 与校验。
func (p *DeckPackage) WriteZip(w io.Writer) error {
	zw := zip.NewWriter(w)
	write := func(name string, v any) error {
		raw, err := json.MarshalIndent(v, "", "  ")
		if err != nil {
			return fmt.Errorf("encode %s: %w", name, err)
		}
		f, err := zw.Create(name)
		if err != nil {
			return fmt.Errorf("create %s: %w", name, err)
		}
		_, err = f.Write(raw)
		return err
	}
	if err := write("manifest.json", p.Manifest); err != nil {
		return err
	}
	if err := write("notes.json", p.Notes); err != nil {
		return err
	}
	if err := write("cards.json", p.Cards); err != nil {
		return err
	}
	if err := write("preset.json", p.Preset); err != nil {
		return err
	}
	if p.Manifest.IncludeProgress && p.Progress != nil {
		if err := write("progress.json", p.Progress); err != nil {
			return err
		}
	}
	if p.Manifest.IncludeMedia && len(p.MediaBytes) > 0 {
		if err := write("media.json", p.Media); err != nil {
			return err
		}
		shas := make([]string, 0, len(p.MediaBytes))
		for sha := range p.MediaBytes {
			shas = append(shas, sha)
		}
		sort.Strings(shas)
		for _, sha := range shas {
			name := p.Media[sha].Path
			f, err := zw.Create(name)
			if err != nil {
				return fmt.Errorf("create %s: %w", name, err)
			}
			if _, err := f.Write(p.MediaBytes[sha]); err != nil {
				return fmt.Errorf("write %s: %w", name, err)
			}
		}
	}
	return zw.Close()
}

// Document 把整包渲染成一个 JSON 文档，键为包内条目名（media/ 条目为 base64 字符串），
// 形状与 schema/deck-package.schema.json 描述的逻辑内容一致，供 schema 校验使用。
func (p *DeckPackage) Document() map[string]any {
	doc := map[string]any{
		"manifest.json": p.Manifest,
		"notes.json":    p.Notes,
		"cards.json":    p.Cards,
		"preset.json":   p.Preset,
	}
	if p.Manifest.IncludeProgress && p.Progress != nil {
		doc["progress.json"] = p.Progress
	}
	if p.Manifest.IncludeMedia && len(p.MediaBytes) > 0 {
		doc["media.json"] = p.Media
		shas := make([]string, 0, len(p.MediaBytes))
		for sha := range p.MediaBytes {
			shas = append(shas, sha)
		}
		sort.Strings(shas)
		for _, sha := range shas {
			doc[p.Media[sha].Path] = base64.StdEncoding.EncodeToString(p.MediaBytes[sha])
		}
	}
	return doc
}

// tagsJSON 把标签切片序列化成 tags_json；nil 时写空数组（字符串默认值在 Go 侧显式给出）。
func tagsJSON(tags []string) string {
	if tags == nil {
		return "[]"
	}
	raw, err := json.Marshal(tags)
	if err != nil {
		return "[]"
	}
	return string(raw)
}

// presetToPackage 把 store.Preset 转成包内 preset（weights_json → []float64 或 null）。
func presetToPackage(p *Preset) PackagePreset {
	out := PackagePreset{
		Name:                p.Name,
		DesiredRetention:    p.DesiredRetention,
		LearningSteps:       p.LearningSteps,
		RelearningSteps:     p.RelearningSteps,
		MaximumIntervalDays: p.MaximumIntervalDays,
		EnableFuzz:          p.FuzzEnabled(),
		WeightsReviewCount:  p.WeightsReviewCount,
	}
	if p.WeightsJSON != nil {
		var w []float64
		if json.Unmarshal([]byte(*p.WeightsJSON), &w) == nil {
			out.Weights = w
		}
	}
	if p.WeightsOptimizedAt != nil {
		s := p.WeightsOptimizedAt.UTC().Format(time.RFC3339)
		out.WeightsOptimizedAt = &s
	}
	return out
}

func cardStateToPackage(cs CardState, noteIndex *int, template string) PackageCardState {
	out := PackageCardState{
		NoteIndex:     noteIndex,
		Template:      template,
		State:         cs.State,
		StepIndex:     cs.StepIndex,
		Stability:     cs.Stability,
		Difficulty:    cs.Difficulty,
		Reps:          cs.Reps,
		Lapses:        cs.Lapses,
		ScheduledDays: cs.ScheduledDays,
		ElapsedDays:   cs.ElapsedDays,
		Version:       cs.Version,
	}
	out.DueAt = formatTimePtr(cs.DueAt)
	out.LastReviewAt = formatTimePtr(cs.LastReviewAt)
	return out
}

func formatTimePtr(t *time.Time) *string {
	if t == nil {
		return nil
	}
	s := t.UTC().Format(time.RFC3339)
	return &s
}

// collectMediaRefs 递归扫描 note 字段里形如 media/<sha256>.<ext> 的引用（DESIGN.md §7.6 示例约定）。
func collectMediaRefs(v any, out map[string]bool) {
	switch x := v.(type) {
	case string:
		if m := mediaRefRE.FindStringSubmatch(x); m != nil {
			out[m[1]] = true
		}
	case map[string]any:
		for _, child := range x {
			collectMediaRefs(child, out)
		}
	case []any:
		for _, child := range x {
			collectMediaRefs(child, out)
		}
	}
}

// NoteFingerprint 是 note 的内容指纹：kind + 规范化字段的 sha256（DESIGN.md §7.6 去重规则）。
// 规范化 = 解析 fields_json 后重新序列化（encoding/json 对 map 键排序），
// 因此同一份内容在不同导出/导入之间得到同一个指纹。
func NoteFingerprint(kind, fieldsJSON string) string {
	canonical := CanonicalFieldsJSON(fieldsJSON)
	sum := sha256.Sum256([]byte(kind + "\n" + canonical))
	return hex.EncodeToString(sum[:])
}

// CanonicalFieldsJSON 把 fields_json 归一成键有序的紧凑 JSON；解析失败时原样返回。
func CanonicalFieldsJSON(fieldsJSON string) string {
	fields, err := ParseFields(fieldsJSON)
	if err != nil {
		return strings.TrimSpace(fieldsJSON)
	}
	raw, err := json.Marshal(fields)
	if err != nil {
		return strings.TrimSpace(fieldsJSON)
	}
	return string(raw)
}

// NewMediaStore 构造媒体元数据存储（只用到按 sha256 查行，供卡组包导出/导入）。
func NewMediaStore(db *gorm.DB) *MediaStore { return &MediaStore{db: db} }

// MediaStore 封装 media 表的元数据访问；字节仍在本地文件系统（DESIGN.md §6.3）。
type MediaStore struct{ db *gorm.DB }

// BySha256 按内容寻址取媒体元数据；不存在返回 (nil, nil)。
func (s *MediaStore) BySha256(ctx context.Context, sha string) (*Media, error) {
	var m Media
	err := s.db.WithContext(ctx).Where("sha256 = ?", sha).First(&m).Error
	if err == nil {
		return &m, nil
	}
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, nil
	}
	return nil, err
}

// SaveBytes 以内容寻址落盘一份媒体并写元数据；已存在（同 sha256）时幂等返回已有行。
func (s *MediaStore) SaveBytes(ctx context.Context, root, mime string, raw []byte, createdBy *uint64) (*Media, error) {
	m, _, err := s.SaveBytesTracked(ctx, root, mime, raw, createdBy)
	return m, err
}

// SaveBytesTracked 与 SaveBytes 相同，但额外返回本次调用**实际新写入**的文件绝对路径；
// 命中已有元数据（去重）或未落盘时返回空串。
//
// 卡组包导入用它登记“这次写了哪些文件”，事务随后失败回滚时据此清理——否则元数据行
// 回滚而字节留在磁盘上，形成无人引用的孤儿文件（AGENTS.md M5-11）。
func (s *MediaStore) SaveBytesTracked(ctx context.Context, root, mime string, raw []byte, createdBy *uint64) (*Media, string, error) {
	sum := sha256.Sum256(raw)
	sha := hex.EncodeToString(sum[:])
	if existing, err := s.BySha256(ctx, sha); err != nil {
		return nil, "", err
	} else if existing != nil {
		return existing, "", nil
	}
	ext := mimeExt(mime)
	rel := filepath.Join(sha[:2], sha+"."+ext)
	abs := filepath.Join(root, rel)
	if err := os.MkdirAll(filepath.Dir(abs), 0o755); err != nil {
		return nil, "", fmt.Errorf("media: create shard dir: %w", err)
	}
	if err := os.WriteFile(abs, raw, 0o644); err != nil {
		return nil, "", fmt.Errorf("media: write file: %w", err)
	}
	row := &Media{Sha256: sha, RelPath: rel, Mime: mime, Bytes: int64(len(raw)), CreatedBy: createdBy, CreatedAt: time.Now().UTC()}
	if err := s.db.WithContext(ctx).Create(row).Error; err != nil {
		if existing, rerr := s.BySha256(ctx, sha); rerr == nil && existing != nil {
			// 并发下另一请求已提交同一 sha：文件被它引用，不算本次新写。
			return existing, "", nil
		}
		// 元数据写入失败：本次写下的文件没有行引用它，把路径交给调用方按失败路径清理。
		return nil, abs, fmt.Errorf("media: record metadata: %w", err)
	}
	return row, abs, nil
}

// mimeExt 把白名单 mime 映射成扩展名；未知类型回退 bin。
func mimeExt(mime string) string {
	switch strings.ToLower(strings.TrimSpace(mime)) {
	case "image/png":
		return "png"
	case "image/jpeg":
		return "jpg"
	case "image/webp":
		return "webp"
	case "image/gif":
		return "gif"
	case "audio/mpeg":
		return "mp3"
	case "audio/ogg":
		return "ogg"
	case "audio/mp4":
		return "m4a"
	default:
		return "bin"
	}
}

// readZipEntry 读取 zip 条目内容（带体积上限，见 package_import.go）。
func readZipEntry(f *zip.File, limit int64) ([]byte, error) {
	rc, err := f.Open()
	if err != nil {
		return nil, err
	}
	defer rc.Close()
	var buf bytes.Buffer
	if _, err := io.Copy(&buf, io.LimitReader(rc, limit+1)); err != nil {
		return nil, err
	}
	if int64(buf.Len()) > limit {
		return nil, fmt.Errorf("%w: entry %s", ErrPackageTooLargeSentinel, f.Name)
	}
	return buf.Bytes(), nil
}

// ErrPackageTooLargeSentinel 是 readZipEntry 的体积超限哨兵；由 ReadPackageArchive 包装成 PackageError。
var ErrPackageTooLargeSentinel = errors.New("package entry exceeds the size limit")
