package store

import (
	"archive/zip"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"

	"git.nite07.com/nite/engram/internal/cardtype"
	"git.nite07.com/nite/engram/internal/mediatype"
)

// 卡组包导入。
//
// 安全是重点：解压前拒绝路径穿越与软链、限制总解压体积与条目数（防 zip bomb）；
// 业务写入全部在一个事务内完成（失败不留半成品）；重复导入靠 external_ref 或内容指纹去重。

// 导入目标三种。
const (
	// PackageTargetNewDeck 用包内卡组名新建卡组（重名自动加后缀）。
	PackageTargetNewDeck = "new_deck"
)

// PackageLimits 是解压安全上限（「安全」）。
type PackageLimits struct {
	MaxEntries    int
	MaxFileBytes  int64
	MaxTotalBytes int64
}

// DefaultPackageLimits 给出保守的默认上限，防 zip bomb。
func DefaultPackageLimits() PackageLimits {
	return PackageLimits{MaxEntries: 2000, MaxFileBytes: 32 << 20, MaxTotalBytes: 128 << 20}
}

// 卡组名与描述的长度界限由 deck.go 统一持有（创建/改名与卡组包导入共用同一来源）。
// 这里保留旧的导出名，避免已经散落的引用漂移。
const (
	maxPackageDeckNameChars        = maxDeckNameChars
	maxPackageDeckDescriptionChars = maxDeckDescriptionChars
)

// deckMetaErrors 校验 manifest 的卡组名与描述，返回条目级原因（英文，与既有条目措辞一致）。
//
// 规则：
//   - 长度按 rune 计：卡组名 ≤ 200、描述 ≤ 2000；
//   - 拒一切 C0 控制字符（U+0000–U+001F），包括 \n、	、\r，不为例外开口子：
//     卡组名/描述没有需要换行的场景，而这些字符会撑坏页面与日志，并干扰 uniqueDeckName 的展示；
//   - 拒非法 UTF-8：后面的 rune 计数与落库都假定输入是合法 UTF-8。
//
// 空值不校验：空卡组名是既有语义（由调用方回退成 Imported deck）。
func deckMetaErrors(name, description string) []string {
	var entries []string
	if name != "" {
		entries = append(entries, textFieldErrors("deck.name", name, maxPackageDeckNameChars)...)
	}
	if description != "" {
		entries = append(entries, textFieldErrors("deck.description", description, maxPackageDeckDescriptionChars)...)
	}
	return entries
}

// textFieldErrors 校验单个文本字段的长度、控制字符与 UTF-8 合法性。
func textFieldErrors(field, value string, maxChars int) []string {
	if !utf8.ValidString(value) {
		// 非法 UTF-8 下 rune 计数没有意义，只报这一条。
		return []string{field + ": invalid UTF-8"}
	}
	var entries []string
	if n := utf8.RuneCountInString(value); n > maxChars {
		entries = append(entries, fmt.Sprintf("%s: exceeds %d characters (got %d)", field, maxChars, n))
	}
	if r, ok := firstControlRune(value); ok {
		entries = append(entries, fmt.Sprintf("%s: contains a control character U+%04X", field, r))
	}
	return entries
}

// firstControlRune 返回值里第一个 C0 控制字符（U+0000–U+001F）。
func firstControlRune(s string) (rune, bool) {
	for _, r := range s {
		if r <= 0x1f {
			return r, true
		}
	}
	return 0, false
}

// packageDeckMetaError 把条目级原因包成稳定的 PackageError；没有原因时返回 nil。
func packageDeckMetaError(entries []string) error {
	if len(entries) == 0 {
		return nil
	}
	return &PackageError{
		Code:    CodePackageDeckMetaInvalid,
		Message: "the deck name or description in the package is invalid",
		Entries: entries,
	}
}

// PackageImportOptions 控制一次导入。
type PackageImportOptions struct {
	// Target 取值 new_deck（默认）、into_deck:<id>、replace_deck:<id>。
	Target string
	DryRun bool
	// OnConflict 取值 skip / update（默认）/ fail（整包回滚）。
	OnConflict string
	// AllowOthersProgress 是管理员开关（默认关）：允许把包内他人进度导入到自己账号。
	AllowOthersProgress bool
	// SkipMissingMedia=true 时缺失媒体只计数并继续；默认 false（缺媒体即失败并列出清单）。
	SkipMissingMedia bool
	// ApplyWeights 默认 off：包内权重是导出者的记忆曲线，导入者默认从默认权重起步，攒够自己的
	// 复习后再优化。只在 new_deck 目标下有作用——合并与替换不动目标卡组现有的预设。
	// 关闭时新预设的 weights_optimized_at 与 weights_review_count 也一并留空。
	ApplyWeights bool
	// MediaQuotaBytes 是导入者当前生效的每用户媒体总量配额（字节）；0 表示不限。
	//
	// 包内新增媒体字节（按 sha256 去重、扣除导入者已计费的 sha）计入**导入者**配额，
	// 超限整包失败、不留部分媒体。取值由调用方从 media.ResolveUserQuotaBytes 解析后传入，
	// 保证 web / REST / MCP / CLI 四条入口只有一处口径。
	MediaQuotaBytes int64
	// MediaRoot 是媒体字节落盘根目录；为空表示不落盘媒体（只在需要时）。
	MediaRoot string
	// NewDeckName 可选覆盖 new_deck 的卡组名。
	NewDeckName string
	// Now 可注入时钟；零值时用系统 UTC 时间。
	Now func() time.Time
}

// PackageImportError 是逐条导入错误：Entry 指出出错的包内条目（如 notes[3]）。
type PackageImportError struct {
	Entry  string `json:"entry"`
	Reason string `json:"reason"`
}

// PackageImportReport 是导入（含 dry_run）的报告，形态与批量导入一致。
type PackageImportReport struct {
	Target string `json:"target"`
	DryRun bool   `json:"dry_run"`
	// DeckID 是内部数字主键，只供审计等内部用途；对外暴露的是 DeckPublicID。
	DeckID            uint64               `json:"-"`
	DeckPublicID      string               `json:"deck_id,omitempty"`
	NotesCreated      int                  `json:"notes_created"`
	NotesUpdated      int                  `json:"notes_updated"`
	NotesSkipped      int                  `json:"notes_skipped"`
	CardsCreated      int                  `json:"cards_created"`
	MediaNew          int                  `json:"media_new"`
	MediaMissing      int                  `json:"media_missing"`
	ProgressApplied   int                  `json:"progress_applied"`
	ProgressSkipped   int                  `json:"progress_skipped"`
	ProgressDiscarded bool                 `json:"progress_discarded"`
	MatchRule         string               `json:"match_rule,omitempty"`
	Errors            []PackageImportError `json:"errors"`
	// WeightsApplied 表示包内权重写进了新建的预设。
	WeightsApplied bool `json:"weights_applied"`
	// WeightsDiscarded 表示包里带了权重但没有采用：未开 ApplyWeights，或目标不是 new_deck。
	WeightsDiscarded bool `json:"weights_discarded"`
}

// ErrPackageDryRun 是 dry_run 的内部哨兵：完成规划后用它回滚事务，不留任何写入。
var ErrPackageDryRun = errors.New("deck package: dry run rollback")

// ReadPackageArchive 安全地读出一个 zip：拒绝路径穿越、绝对路径、软链，
// 并限制条目数与总解压体积（「安全」）。
func ReadPackageArchive(r io.Reader, limits PackageLimits) (map[string][]byte, error) {
	// 先整体读入内存（有总量上限），因为 zip.NewReader 需要 ReaderAt。
	raw, err := io.ReadAll(io.LimitReader(r, limits.MaxTotalBytes+1))
	if err != nil {
		return nil, fmt.Errorf("%w: read archive: %v", &PackageError{Code: CodePackageBadFormat, Message: "cannot read archive"}, err)
	}
	if int64(len(raw)) > limits.MaxTotalBytes {
		return nil, &PackageError{Code: CodePackageTooLarge, Message: "archive exceeds the total size limit"}
	}
	zr, err := zip.NewReader(bytes.NewReader(raw), int64(len(raw)))
	if err != nil {
		return nil, &PackageError{Code: CodePackageBadFormat, Message: "not a valid zip archive"}
	}
	if len(zr.File) > limits.MaxEntries {
		return nil, &PackageError{Code: CodePackageTooLarge, Message: fmt.Sprintf("archive has %d entries, limit is %d", len(zr.File), limits.MaxEntries)}
	}
	out := make(map[string][]byte, len(zr.File))
	var total int64
	for _, f := range zr.File {
		name := f.Name
		if strings.HasSuffix(name, "/") || f.FileInfo().IsDir() {
			continue
		}
		// 同名条目（重复路径）必须拒绝。zip 允许同一名字出现多次，而这里按名字存入
		// map——若不拦，后一个条目会静默覆盖前一个，包因此能藏一个与索引不符的覆盖层。
		if _, dup := out[name]; dup {
			return nil, &PackageError{Code: CodePackageBadFormat, Message: "archive contains a duplicate entry", Entries: []string{name}}
		}
		if f.Mode()&os.ModeSymlink != 0 {
			return nil, &PackageError{Code: CodePackageUnsafeEntry, Message: "symlink entries are not allowed", Entries: []string{name}}
		}
		if unsafeZipName(name) {
			return nil, &PackageError{Code: CodePackageUnsafeEntry, Message: "unsafe entry path", Entries: []string{name}}
		}
		buf, err := readZipEntry(f, limits.MaxFileBytes)
		if err != nil {
			if errors.Is(err, ErrPackageTooLargeSentinel) {
				return nil, &PackageError{Code: CodePackageTooLarge, Message: "entry exceeds the size limit", Entries: []string{name}}
			}
			return nil, &PackageError{Code: CodePackageBadFormat, Message: "cannot read entry", Entries: []string{name}}
		}
		total += int64(len(buf))
		if total > limits.MaxTotalBytes {
			return nil, &PackageError{Code: CodePackageTooLarge, Message: "archive exceeds the total decompressed size limit"}
		}
		out[name] = buf
	}
	return out, nil
}

// unsafeZipName 判断 zip 条目名是否危险：绝对路径、反斜杠、盘符、或含 ".." 段。
func unsafeZipName(name string) bool {
	if name == "" {
		return true
	}
	if strings.Contains(name, "\\") {
		return true
	}
	if strings.HasPrefix(name, "/") || filepath.IsAbs(name) || path.IsAbs(name) {
		return true
	}
	if len(name) >= 2 && name[1] == ':' {
		return true
	}
	for _, seg := range strings.Split(name, "/") {
		if seg == ".." {
			return true
		}
	}
	return false
}

// ParsePackageTargetRef 解析目标串里的种类与卡组标识原文（客户端给的是对外 id）；
// 空串等同 new_deck。标识的合法性由调用方判定——只有它知道该标识是对外 id 还是内部主键。
func ParsePackageTargetRef(target string) (kind string, ref string, err error) {
	target = strings.TrimSpace(target)
	if target == "" || target == PackageTargetNewDeck {
		return PackageTargetNewDeck, "", nil
	}
	for _, prefix := range []string{"into_deck:", "replace_deck:"} {
		if strings.HasPrefix(target, prefix) {
			ref := strings.TrimSpace(strings.TrimPrefix(target, prefix))
			if ref == "" {
				return "", "", &PackageError{Code: CodePackageBadFormat, Message: "target deck id is empty"}
			}
			return strings.TrimSuffix(prefix, ":"), ref, nil
		}
	}
	return "", "", &PackageError{Code: CodePackageBadFormat, Message: "target must be new_deck, into_deck:<id> or replace_deck:<id>"}
}

// ParsePackageTarget 解析 store 内部使用的目标串（卡组标识是数字主键）；空串等同 new_deck。
func ParsePackageTarget(target string) (kind string, deckID uint64, err error) {
	kind, ref, err := ParsePackageTargetRef(target)
	if err != nil || kind == PackageTargetNewDeck {
		return kind, 0, err
	}
	id, perr := strconv.ParseUint(ref, 10, 64)
	if perr != nil || id == 0 {
		return "", 0, &PackageError{Code: CodePackageBadFormat, Message: "target deck id must be a positive integer"}
	}
	return kind, id, nil
}

// FormatPackageTarget 组装 store 内部形态的目标串；由传输层把对外 id 解析成数字主键后调用。
func FormatPackageTarget(kind string, deckID uint64) string {
	if kind == "" || kind == PackageTargetNewDeck {
		return PackageTargetNewDeck
	}
	return kind + ":" + strconv.FormatUint(deckID, 10)
}

// deckPublicID 取某卡组的对外 id；导入报告带的是对外 id 而不是数字主键。
func deckPublicID(ctx context.Context, tx *gorm.DB, deckID uint64) (string, error) {
	var pid string
	if err := tx.WithContext(ctx).Table("decks").Select("public_id").Where("id = ?", deckID).Scan(&pid).Error; err != nil {
		return "", fmt.Errorf("load deck public id: %w", err)
	}
	return pid, nil
}

// ImportPackage 导入一个卡组包。
//
// 调用方（API/Web/MCP/CLI）负责在调用前完成卡组级权限判定；本方法只做包级校验、
// 去重、id 重映射、进度归属判定与事务化写入。
func (s *DeckStore) ImportPackage(ctx context.Context, actorUserID uint64, r io.Reader, opts PackageImportOptions) (*PackageImportReport, error) {
	// 本次导入的执行者：note 写入的写前校验据此判断「新引入的引用是否导入者可读」
	// 包内提供的媒体字节会先登记到 media_uploaders，再校验。
	ctx = WithActor(ctx, actorUserID)
	if opts.Now == nil {
		opts.Now = func() time.Time { return time.Now().UTC() }
	}
	if opts.OnConflict == "" {
		opts.OnConflict = "update"
	}
	if opts.OnConflict != "skip" && opts.OnConflict != "update" && opts.OnConflict != "fail" {
		return nil, &PackageError{Code: CodePackageBadFormat, Message: "on_conflict must be one of skip, update, fail"}
	}
	targetKind, targetDeckID, err := ParsePackageTarget(opts.Target)
	if err != nil {
		return nil, err
	}

	entries, err := ReadPackageArchive(r, DefaultPackageLimits())
	if err != nil {
		return nil, err
	}
	pkg, err := parsePackage(entries)
	if err != nil {
		return nil, err
	}

	// 未知题型必须报错并逐条列出（静默丢弃会让用户以为导入成功却少了卡片）。
	var unknown []string
	for i := range pkg.Notes {
		if _, ok := cardtype.Lookup(pkg.Notes[i].Kind); !ok {
			unknown = append(unknown, fmt.Sprintf("notes[%d] kind=%s", i, pkg.Notes[i].Kind))
		}
	}
	if len(unknown) > 0 {
		return nil, &PackageError{Code: CodePackageUnknownKind, Message: "unknown card type", Entries: unknown}
	}

	// 媒体以真实字节为准做白名单与声明交叉校验；放在事务之前，dry_run 与真实导入同样被拒，
	// 且任何文件都还没落盘。
	if err := validatePackageMedia(pkg); err != nil {
		return nil, err
	}

	// 导入新增的媒体字节计入**导入者**配额，超限整包失败。检查同样放在事务与任何
	// 写盘之前，因此被拒时库里没有卡组/卡/媒体行，磁盘上也没有字节。
	if err := checkImportMediaQuota(ctx, s.db, actorUserID, pkg, opts.MediaQuotaBytes); err != nil {
		return nil, err
	}

	username, _ := s.lookupUsername(ctx, actorUserID)

	report := &PackageImportReport{Target: targetKind, DryRun: opts.DryRun, Errors: []PackageImportError{}}
	mediaMissing, err := missingMediaRefs(pkg)
	if err != nil {
		return nil, err
	}
	if len(mediaMissing) > 0 && !opts.SkipMissingMedia && !opts.DryRun {
		return nil, &PackageError{Code: CodePackageBadFormat, Message: "package references media that is not included", Entries: mediaMissing}
	}
	report.MediaMissing = len(mediaMissing)

	// writtenMedia 记录本次事务实际落盘的媒体文件；事务失败回滚时据此清理，
	// 避免元数据行回滚而字节留在磁盘上形成孤儿文件。
	var writtenMedia []mediaWrite
	err = s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		return s.importInTx(ctx, tx, actorUserID, username, pkg, targetKind, targetDeckID, opts, report, &writtenMedia)
	})
	if err != nil {
		s.cleanupMediaWrites(ctx, writtenMedia)
	}
	if err != nil && !errors.Is(err, ErrPackageDryRun) {
		return nil, err
	}
	if err == nil && opts.DryRun {
		return nil, ErrPackageDryRun
	}
	return report, nil
}

// mediaWrite 是本次导入新写入的一个媒体文件（sha + 绝对路径）。
type mediaWrite struct {
	sha string
	abs string
}

// cleanupMediaWrites 删除本次导入已落盘、却因事务回滚而无元数据引用的媒体文件。
//
// 只删本进程本次写下的文件；若该 sha 已被（并发的）其它已提交事务引用了元数据行，
// 则保留文件——它不再是孤儿。删除失败只忽略（文件已不在或权限问题都不影响正确性，
// 最坏情况是退回修复前的行为）。
func (s *DeckStore) cleanupMediaWrites(ctx context.Context, writes []mediaWrite) {
	if len(writes) == 0 {
		return
	}
	store := NewMediaStore(s.db)
	for _, w := range writes {
		if row, err := store.BySha256(ctx, w.sha); err == nil && row != nil {
			continue
		}
		if err := os.Remove(w.abs); err != nil && !errors.Is(err, os.ErrNotExist) {
			_ = err
		}
	}
}

// packageModel 是解析后的包内容。
type packageModel struct {
	Manifest PackageManifest
	Notes    []PackageNote
	Cards    []PackageCard
	Preset   PackagePreset
	Progress *PackageProgress
	Media    map[string]PackageMediaEntry
	MediaRaw map[string][]byte
}

func parseJSONEntry(entries map[string][]byte, name string, out any) error {
	raw, ok := entries[name]
	if !ok {
		return &PackageError{Code: CodePackageBadFormat, Message: "missing required entry", Entries: []string{name}}
	}
	if err := json.Unmarshal(raw, out); err != nil {
		return &PackageError{Code: CodePackageBadFormat, Message: "invalid JSON", Entries: []string{name + ": " + err.Error()}}
	}
	return nil
}

func parsePackage(entries map[string][]byte) (*packageModel, error) {
	pkg := &packageModel{Media: map[string]PackageMediaEntry{}, MediaRaw: map[string][]byte{}}
	// manifest.json 的原始字节必须是合法 UTF-8：encoding/json 会把非法字节静默替换成
	// U+FFFD（按文档“不是错误”），只看解码后的字符串就永远抓不到它，只能在原始字节上把关。
	if raw, ok := entries["manifest.json"]; ok && !utf8.Valid(raw) {
		return nil, &PackageError{Code: CodePackageBadFormat, Message: "invalid UTF-8 in manifest", Entries: []string{"manifest.json: invalid UTF-8"}}
	}
	if err := parseJSONEntry(entries, "manifest.json", &pkg.Manifest); err != nil {
		return nil, err
	}
	// 卡组名/描述的长度与字符界限：在解析出 manifest 后立即校验，事务开始前拒绝整包。
	if err := packageDeckMetaError(deckMetaErrors(pkg.Manifest.Deck.Name, pkg.Manifest.Deck.Description)); err != nil {
		return nil, err
	}
	if pkg.Manifest.FormatVersion < 1 || pkg.Manifest.FormatVersion > PackageFormatVersion {
		return nil, &PackageError{Code: CodePackageBadFormat, Message: fmt.Sprintf("unsupported format_version %d", pkg.Manifest.FormatVersion)}
	}
	if err := parseJSONEntry(entries, "notes.json", &pkg.Notes); err != nil {
		return nil, err
	}
	if err := parseJSONEntry(entries, "cards.json", &pkg.Cards); err != nil {
		return nil, err
	}
	if err := parseJSONEntry(entries, "preset.json", &pkg.Preset); err != nil {
		return nil, err
	}
	if raw, ok := entries["progress.json"]; ok {
		var p PackageProgress
		if err := json.Unmarshal(raw, &p); err != nil {
			return nil, &PackageError{Code: CodePackageBadFormat, Message: "invalid JSON", Entries: []string{"progress.json: " + err.Error()}}
		}
		pkg.Progress = &p
	}
	if raw, ok := entries["media.json"]; ok {
		if err := json.Unmarshal(raw, &pkg.Media); err != nil {
			return nil, &PackageError{Code: CodePackageBadFormat, Message: "invalid JSON", Entries: []string{"media.json: " + err.Error()}}
		}
		for sha, entry := range pkg.Media {
			// 只接受包内 media/ 前缀且文件名与 sha256 一致的条目，挡住 media.json 里的路径穿越。
			if unsafeZipName(entry.Path) || !strings.HasPrefix(entry.Path, "media/") {
				return nil, &PackageError{Code: CodePackageUnsafeEntry, Message: "media path is unsafe", Entries: []string{entry.Path}}
			}
			// 文件名必须是 `<sha256>.<ext>`，否则声明的 sha 与条目名对不上（索引与内容脱钩）。
			base := path.Base(entry.Path)
			if !strings.HasPrefix(base, sha+".") || len(base) <= len(sha)+1 {
				return nil, &PackageError{Code: CodePackageBadFormat, Message: "media entry name does not match the declared sha256", Entries: []string{entry.Path}}
			}
			raw, ok := entries[entry.Path]
			if !ok {
				continue
			}
			// 声明的 sha256 必须等于字节的真实 sha256，挡住"假 sha"（声明与内容不符）。
			// 这一校验在任何落盘/写库之前完成，因此被拒的包零副作用。
			sum := sha256.Sum256(raw)
			if hex.EncodeToString(sum[:]) != sha {
				return nil, &PackageError{Code: CodePackageBadFormat, Message: "media bytes do not match the declared sha256", Entries: []string{entry.Path}}
			}
			pkg.MediaRaw[sha] = raw
		}
	}
	return pkg, nil
}

// missingMediaRefs 找出 note 字段里引用、但包内没有字节的媒体。
func missingMediaRefs(pkg *packageModel) ([]string, error) {
	refs := map[string]bool{}
	for i := range pkg.Notes {
		collectMediaRefs(pkg.Notes[i].Fields, refs)
	}
	var missing []string
	for sha := range refs {
		if _, ok := pkg.MediaRaw[sha]; !ok {
			missing = append(missing, "media/"+sha)
		}
	}
	return missing, nil
}

// headBytes 取文件头，最多 mediatype.HeadBytes 字节；判定所需长度以内有多少给多少。
func headBytes(raw []byte) []byte {
	if len(raw) > mediatype.HeadBytes {
		return raw[:mediatype.HeadBytes]
	}
	return raw
}

// validatePackageMedia 在写任何文件之前，用真实字节判定包内每份媒体的类型：
// 不在白名单内即拒，且与 media.json 的声明交叉校验——声明与字节不符即拒。
//
// 为什么放在 store 层而不是复用 internal/media.Save：media 包已 import store，
// 反向依赖会成环；Save 还用自己那条数据库连接，绕开导入事务，在 SQLite 上会造成
// SQLITE_BUSY。判定逻辑本身在叶子包 mediatype 里与上传链共用，这里只做包级编排。
//
// 声明缺字节的条目交给 missingMediaRefs 报告，这里跳过，避免同一问题报两次。
func validatePackageMedia(pkg *packageModel) error {
	var entries []string
	for sha, entry := range pkg.Media {
		raw, ok := pkg.MediaRaw[sha]
		if !ok {
			continue
		}
		detected, _, ok := mediatype.Detect(headBytes(raw))
		if !ok {
			entries = append(entries, fmt.Sprintf("media/%s: unrecognised media bytes", sha))
			continue
		}
		if !mediatype.Allowed(detected, mediatype.DefaultAllowedMimes()) {
			entries = append(entries, fmt.Sprintf("media/%s: detected %s is not an allowed media type", sha, detected))
			continue
		}
		if declared := mediatype.Normalize(entry.Mime); declared != "" && declared != detected {
			entries = append(entries, fmt.Sprintf("media/%s: declared %s does not match detected %s", sha, declared, detected))
		}
	}
	if len(entries) == 0 {
		return nil
	}
	// 逐条按字典序，保证同一包每次报错顺序一致。
	sort.Strings(entries)
	return &PackageError{Code: CodePackageUnsafeMedia, Message: "package media failed validation", Entries: entries}
}

// checkImportMediaQuota 在写任何文件与数据库行之前，检查导入新增媒体是否超出导入者配额。
//
// 计量口径与上传链（internal/store.UserMediaUsage）一致：
//   - 已用量 = 该用户 note 引用到的媒体 ∪ 该用户尚无人引用的上传，按 sha256 去重求和；
//   - 本次新增 = 包内媒体里该用户**尚未计费**的 sha 的字节之和（同一 blob 已计费则不重复收费，
//     与上传端「同 sha 新增量为 0」同规）。
//
// quota<=0 表示不限，直接放行。超限返回 CodePackageQuotaExceeded，调用方在事务之前返回，
// 因此不会留下任何卡组/卡/媒体行或磁盘文件。
func checkImportMediaQuota(ctx context.Context, db *gorm.DB, actorUserID uint64, pkg *packageModel, quota int64) error {
	if quota <= 0 {
		return nil
	}
	usage, err := UserMediaUsage(ctx, db, actorUserID)
	if err != nil {
		return fmt.Errorf("import package: media usage: %w", err)
	}
	var extra int64
	for sha, raw := range pkg.MediaRaw {
		if usage.Sha256[sha] {
			continue
		}
		extra += int64(len(raw))
	}
	if usage.Bytes+extra <= quota {
		return nil
	}
	return &PackageError{Code: CodePackageQuotaExceeded, Message: "package media would exceed this user's media quota"}
}

// importInTx 在一个事务里完成全部写入；dry_run 时由调用方以 ErrPackageDryRun 回滚。
func (s *DeckStore) importInTx(ctx context.Context, tx *gorm.DB, actorUserID uint64, username string, pkg *packageModel, targetKind string, targetDeckID uint64, opts PackageImportOptions, report *PackageImportReport, writtenMedia *[]mediaWrite) error {
	// 包内的媒体字节视为「本次由导入者提供」：先登记到 media_uploaders，再做 note 写入的
	// 写前校验（先把本次提供的 sha 写入 media_uploaders 再校验）。否则
	// 「导入自己刚提供的字节」会被写前校验误判成越权引用。dry_run 不落任何行。
	if !opts.DryRun && opts.MediaRoot != "" {
		for sha := range pkg.MediaRaw {
			if err := RecordMediaUploader(ctx, tx, sha, actorUserID); err != nil {
				return fmt.Errorf("import package: record media uploader: %w", err)
			}
		}
	}
	deckID, err := s.resolveTargetDeck(ctx, tx, actorUserID, pkg, targetKind, targetDeckID, opts)
	if err != nil {
		return err
	}
	report.DeckID = deckID
	if len(pkg.Preset.Weights) > 0 {
		report.WeightsApplied = targetKind == PackageTargetNewDeck && opts.ApplyWeights
		report.WeightsDiscarded = !report.WeightsApplied
	}
	pid, err := deckPublicID(ctx, tx, deckID)
	if err != nil {
		return err
	}
	report.DeckPublicID = pid

	// replace_deck 是破坏性操作：先软删除目标卡组全部现有 note（进度保留在 card 上，不级联删行）。
	if targetKind == "replace_deck" {
		if err := tx.WithContext(ctx).Where("deck_id = ?", deckID).Delete(&Note{}).Error; err != nil {
			return fmt.Errorf("replace deck: clear notes: %w", err)
		}
	}

	// 现有内容索引：external_ref 优先，其次内容指纹（去重规则）。
	byRef, byFingerprint, err := s.existingNoteIndex(ctx, tx, deckID)
	if err != nil {
		return err
	}
	if len(byRef) > 0 {
		report.MatchRule = "external_ref"
	} else {
		report.MatchRule = "content_fingerprint"
	}

	notesStore := NewNoteStore(s.db)
	noteIDByIndex := make([]uint64, len(pkg.Notes))
	createdNote := make([]bool, len(pkg.Notes))

	for i := range pkg.Notes {
		pn := &pkg.Notes[i]
		fp := fingerprintFromFields(pn.Kind, pn.Fields)
		var existing *Note
		if pn.ExternalRef != "" {
			existing = byRef[pn.ExternalRef]
		}
		if existing == nil {
			existing = byFingerprint[fp]
		}

		if existing != nil {
			switch opts.OnConflict {
			case "skip":
				report.NotesSkipped++
				noteIDByIndex[i] = existing.ID
				continue
			case "fail":
				report.Errors = append(report.Errors, PackageImportError{fmt.Sprintf("notes[%d]", i), "a matching note already exists"})
				continue
			}
			// update：保留 card 行与所有人进度，只改内容（NoteStore.SaveInTx 保证 id 不变）。
			n := Note{ID: existing.ID, Kind: pn.Kind, TagsJSON: TagsJSON(pn.Tags)}
			if _, err := notesStore.SaveInTx(ctx, tx, &n, pn.Fields); err != nil {
				return fmt.Errorf("import note %d: %w", i, asPackageMediaForbidden(err))
			}
			report.NotesUpdated++
			noteIDByIndex[i] = existing.ID
			continue
		}

		n := Note{DeckID: deckID, Kind: pn.Kind, TagsJSON: TagsJSON(pn.Tags), CreatedBy: Ptr(actorUserID), Source: Ptr("import")}
		if pn.ExternalRef != "" {
			n.ExternalRef = Ptr(pn.ExternalRef)
		}
		cards, err := notesStore.SaveInTx(ctx, tx, &n, pn.Fields)
		if err != nil {
			return fmt.Errorf("import note %d: %w", i, asPackageMediaForbidden(err))
		}
		report.NotesCreated++
		report.CardsCreated += len(cards)
		noteIDByIndex[i] = n.ID
		createdNote[i] = true
	}

	// cards.json 保证导入后卡片集合一致：核对包内声明的 template 是否都已生成。
	cardIDByNote := map[uint64]map[string]uint64{}
	for i := range pkg.Notes {
		if noteIDByIndex[i] == 0 {
			continue
		}
		m, err := cardTemplatesTx(ctx, tx, noteIDByIndex[i])
		if err != nil {
			return err
		}
		cardIDByNote[noteIDByIndex[i]] = m
	}
	for _, pc := range pkg.Cards {
		if pc.NoteIndex < 0 || pc.NoteIndex >= len(pkg.Notes) {
			report.Errors = append(report.Errors, PackageImportError{fmt.Sprintf("cards[%s]", pc.Template), "note_index out of range"})
			continue
		}
		m := cardIDByNote[noteIDByIndex[pc.NoteIndex]]
		if _, ok := m[pc.Template]; !ok {
			report.Errors = append(report.Errors, PackageImportError{fmt.Sprintf("cards[%s]", pc.Template), "declared template was not produced"})
		}
	}

	// 媒体：按 sha256 落盘（已存在则跳过）；dry_run 只计数。
	//
	// 必须走事务句柄 tx 而不是 s.db：s.db 是另一条连接，在 SQLite 上会被外层写事务的
	// 文件锁挡在外面（SQLITE_BUSY: database is locked）——这正是带媒体的包导入在 SQLite
	// 下必然失败的原因（PostgreSQL 因允许多连接并发写而侥幸通过，掩盖了缺陷）。
	// 用 tx 后，媒体元数据与卡组内容在同一事务里原子可见；dry_run 则连文件都不落盘。
	for sha, raw := range pkg.MediaRaw {
		if opts.MediaRoot == "" {
			continue
		}
		mstore := NewMediaStore(tx)
		existing, err := mstore.BySha256(ctx, sha)
		if err != nil {
			return fmt.Errorf("import package: check media %s: %w", sha, err)
		}
		if existing != nil {
			continue
		}
		report.MediaNew++
		if opts.DryRun {
			continue
		}
		// 落库的 mime 以字节判定为准（validatePackageMedia 已确认它是白名单类型）；
		// media.json 的声明只作交叉校验，绝不当真写进 media 表。
		detected, _, _ := mediatype.Detect(headBytes(raw))
		_, abs, err := mstore.SaveBytesTracked(ctx, opts.MediaRoot, detected, raw, Ptr(actorUserID))
		if abs != "" {
			// 先登记路径再判错：SaveBytesTracked 可能在写文件成功、写元数据失败时同时
			// 返回路径与错误，这种情况下文件同样需要被失败路径清理。
			*writtenMedia = append(*writtenMedia, mediaWrite{sha: sha, abs: abs})
		}
		if err != nil {
			return err
		}
	}

	// 进度规则：只允许导入到自己的账号；包内 exported_by 与当前用户不同则默认丢弃并告知。
	if pkg.Progress != nil && len(pkg.Progress.CardStates) > 0 {
		sameOwner := pkg.Manifest.ExportedBy == "" || pkg.Manifest.ExportedBy == username
		if !sameOwner && !opts.AllowOthersProgress {
			report.ProgressDiscarded = true
			report.ProgressSkipped = len(pkg.Progress.CardStates)
		} else {
			if err := s.applyProgress(ctx, tx, actorUserID, pkg, noteIDByIndex, cardIDByNote, createdNote, report); err != nil {
				return err
			}
		}
	}

	if opts.OnConflict == "fail" && len(report.Errors) > 0 {
		return &PackageError{Code: CodePackageBadFormat, Message: "import aborted: one or more entries conflict or are invalid", Entries: errorEntries(report.Errors)}
	}
	if opts.DryRun {
		return ErrPackageDryRun
	}
	return nil
}

// resolveTargetDeck 解析三种目标并返回目标卡组 id；new_deck 会用包内卡组名建卡组与预设。
func (s *DeckStore) resolveTargetDeck(ctx context.Context, tx *gorm.DB, actorUserID uint64, pkg *packageModel, targetKind string, targetDeckID uint64, opts PackageImportOptions) (uint64, error) {
	switch targetKind {
	case PackageTargetNewDeck:
		name := opts.NewDeckName
		if name == "" {
			name = pkg.Manifest.Deck.Name
		}
		if name == "" {
			name = "Imported deck"
		}
		// 覆盖值（opts.NewDeckName）可能与 manifest 不同：界限对最终写进 decks.name 的那个值同样生效，
		// 否则调用方能用覆盖绕开 manifest 校验。
		if err := packageDeckMetaError(textFieldErrors("deck.name", name, maxPackageDeckNameChars)); err != nil {
			return 0, err
		}
		name, err := uniqueDeckName(ctx, tx, actorUserID, name)
		if err != nil {
			return 0, err
		}
		presetID, err := createPresetFromPackage(ctx, tx, actorUserID, &pkg.Preset, opts.ApplyWeights)
		if err != nil {
			return 0, err
		}
		d := Deck{
			OwnerUserID: actorUserID, Name: name, Description: pkg.Manifest.Deck.Description,
			PresetID: presetID, CreatedAt: opts.Now().UTC(),
		}
		if err := tx.WithContext(ctx).Create(&d).Error; err != nil {
			return 0, fmt.Errorf("import package: create deck: %w", err)
		}
		return d.ID, nil
	case "into_deck", "replace_deck":
		// 调用方（REST/MCP/Web/CLI）必须已对目标卡组完成判权：合并需 editor，
		// 替换是破坏性操作需 owner。store 层不重复判权，也无 actor 角色可判。
		var d Deck
		if err := tx.WithContext(ctx).First(&d, "id = ?", targetDeckID).Error; err != nil {
			return 0, &PackageError{Code: CodePackageBadFormat, Message: "target deck not found"}
		}
		return d.ID, nil
	default:
		return 0, &PackageError{Code: CodePackageBadFormat, Message: "unknown target"}
	}
}

// uniqueDeckName 在重名时追加 (2)、(3)… 后缀。
func uniqueDeckName(ctx context.Context, tx *gorm.DB, owner uint64, name string) (string, error) {
	var names []string
	if err := tx.WithContext(ctx).Model(&Deck{}).Where("owner_user_id = ?", owner).
		Pluck("name", &names).Error; err != nil {
		return "", fmt.Errorf("list deck names: %w", err)
	}
	taken := map[string]bool{}
	for _, n := range names {
		taken[n] = true
	}
	if !taken[name] {
		return name, nil
	}
	for i := 2; ; i++ {
		candidate := fmt.Sprintf("%s (%d)", name, i)
		if !taken[candidate] {
			return candidate, nil
		}
	}
}

// createPresetFromPackage 用包内参数新建一个属于 actorUserID 的预设。
// applyWeights 为 false 时不写权重及其两项元数据：导入者的复习日志里没有拟合这组权重的那些复习，
// 留下 weights_review_count 会让预设显示一个在本账号查不到来源的优化记录。
func createPresetFromPackage(ctx context.Context, tx *gorm.DB, owner uint64, pp *PackagePreset, applyWeights bool) (uint64, error) {
	now := time.Now().UTC()
	p := Preset{
		OwnerUserID: owner, Name: pp.Name, DesiredRetention: pp.DesiredRetention,
		LearningSteps: pp.LearningSteps, RelearningSteps: pp.RelearningSteps,
		MaximumIntervalDays: pp.MaximumIntervalDays, EnableFuzz: Ptr(pp.EnableFuzz),
		CreatedAt: now, UpdatedAt: now,
	}
	if p.Name == "" {
		p.Name = "Imported"
	}
	if p.DesiredRetention <= 0 || p.DesiredRetention > 1 {
		p.DesiredRetention = DefaultDesiredRetention
	}
	if p.MaximumIntervalDays <= 0 {
		p.MaximumIntervalDays = DefaultMaximumIntervalDays
	}
	if applyWeights && len(pp.Weights) > 0 {
		raw, err := json.Marshal(pp.Weights)
		if err != nil {
			return 0, err
		}
		s := string(raw)
		p.WeightsJSON = &s
		p.WeightsReviewCount = pp.WeightsReviewCount
		if pp.WeightsOptimizedAt != nil {
			if t, err := time.Parse(time.RFC3339, *pp.WeightsOptimizedAt); err == nil {
				p.WeightsOptimizedAt = &t
			}
		}
	}
	if err := tx.WithContext(ctx).Create(&p).Error; err != nil {
		return 0, fmt.Errorf("import package: create preset: %w", err)
	}
	return p.ID, nil
}

// existingNoteIndex 建立目标卡组的现有 note 索引：external_ref → note，指纹 → note。
func (s *DeckStore) existingNoteIndex(ctx context.Context, tx *gorm.DB, deckID uint64) (map[string]*Note, map[string]*Note, error) {
	var notes []Note
	if err := tx.WithContext(ctx).Where("deck_id = ?", deckID).Find(&notes).Error; err != nil {
		return nil, nil, fmt.Errorf("load existing notes: %w", err)
	}
	byRef := map[string]*Note{}
	byFP := map[string]*Note{}
	for i := range notes {
		n := &notes[i]
		if n.ExternalRef != nil && *n.ExternalRef != "" {
			byRef[*n.ExternalRef] = n
		}
		byFP[NoteFingerprint(n.Kind, n.FieldsJSON)] = n
	}
	return byRef, byFP, nil
}

// fingerprintFromFields 计算包内 note 的内容指纹（与 NoteFingerprint 同一算法）。
func fingerprintFromFields(kind string, fields map[string]any) string {
	raw, err := json.Marshal(fields)
	if err != nil {
		raw = []byte("{}")
	}
	return NoteFingerprint(kind, string(raw))
}

// cardTemplatesTx 返回某 note 的 template → card id 映射（含软删除行）。
func cardTemplatesTx(ctx context.Context, tx *gorm.DB, noteID uint64) (map[string]uint64, error) {
	var cards []Card
	if err := tx.WithContext(ctx).Unscoped().Where("note_id = ?", noteID).Find(&cards).Error; err != nil {
		return nil, fmt.Errorf("list cards for note %d: %w", noteID, err)
	}
	m := make(map[string]uint64, len(cards))
	for _, c := range cards {
		m[c.Template] = c.ID
	}
	return m, nil
}

// applyProgress 把包内进度映射到新 card 上，只写入 actorUserID 自己的账号。
func (s *DeckStore) applyProgress(ctx context.Context, tx *gorm.DB, actorUserID uint64, pkg *packageModel, noteIDByIndex []uint64, cardIDByNote map[uint64]map[string]uint64, createdNote []bool, report *PackageImportReport) error {
	// external_ref → note_index（进度也可以只用 external_ref 定位）。
	refIndex := map[string]int{}
	for i := range pkg.Notes {
		if pkg.Notes[i].ExternalRef != "" {
			refIndex[pkg.Notes[i].ExternalRef] = i
		}
	}
	resolve := func(noteIndex *int, ref, template string) (uint64, bool) {
		idx := -1
		if noteIndex != nil {
			idx = *noteIndex
		} else if ref != "" {
			if v, ok := refIndex[ref]; ok {
				idx = v
			}
		}
		if idx < 0 || idx >= len(noteIDByIndex) || noteIDByIndex[idx] == 0 {
			return 0, false
		}
		m := cardIDByNote[noteIDByIndex[idx]]
		if m == nil {
			return 0, false
		}
		// template 缺省时取第一张卡（进度条目允许省略 template）。
		if template == "" {
			for _, id := range m {
				return id, true
			}
			return 0, false
		}
		id, ok := m[template]
		return id, ok
	}

	for _, ps := range pkg.Progress.CardStates {
		cardID, ok := resolve(ps.NoteIndex, ps.ExternalRef, ps.Template)
		if !ok {
			report.ProgressSkipped++
			continue
		}
		row := CardState{
			CardID: cardID, UserID: actorUserID, State: ps.State,
			StepIndex: ps.StepIndex, Stability: ps.Stability, Difficulty: ps.Difficulty,
			Reps: ps.Reps, Lapses: ps.Lapses, ScheduledDays: ps.ScheduledDays,
			ElapsedDays: ps.ElapsedDays, Version: ps.Version,
		}
		row.DueAt = parseTimePtr(ps.DueAt)
		row.LastReviewAt = parseTimePtr(ps.LastReviewAt)
		if err := tx.WithContext(ctx).Clauses(clause.OnConflict{
			Columns:   []clause.Column{{Name: "card_id"}, {Name: "user_id"}},
			DoUpdates: clause.AssignmentColumns([]string{"state", "due_at", "step_index", "stability", "difficulty", "reps", "lapses", "scheduled_days", "elapsed_days", "last_review_at", "version"}),
		}).Create(&row).Error; err != nil {
			return fmt.Errorf("apply progress for card %d: %w", cardID, err)
		}
		report.ProgressApplied++
	}

	// 复习日志只随新建的 note 写入，避免同一包二次导入时重复插入 append-only 行。
	for _, pr := range pkg.Progress.Reviews {
		idx := -1
		if pr.NoteIndex != nil {
			idx = *pr.NoteIndex
		} else if pr.ExternalRef != "" {
			if v, ok := refIndex[pr.ExternalRef]; ok {
				idx = v
			}
		}
		if idx < 0 || idx >= len(pkg.Notes) || !createdNote[idx] {
			continue
		}
		cardID, ok := resolve(&idx, "", pr.Template)
		if !ok {
			continue
		}
		reviewedAt, err := time.Parse(time.RFC3339, pr.ReviewedAt)
		if err != nil {
			continue
		}
		src := pr.GradeSource
		if src == "" {
			src = "self"
		}
		row := Review{
			CardID: cardID, UserID: actorUserID, Rating: pr.Rating, GradeSource: src,
			ReviewedAt: reviewedAt.UTC(), ReviewDay: pr.ReviewDay, ElapsedMS: pr.ElapsedMS,
			DurationDays: pr.DurationDays, StateBefore: pr.StateBefore,
			StepIndexBefore: pr.StepIndexBefore, DueBefore: parseTimePtr(pr.DueBefore),
			IntervalDays: pr.IntervalDays, Stability: pr.Stability, Difficulty: pr.Difficulty,
		}
		if row.ReviewDay == "" {
			row.ReviewDay = reviewedAt.UTC().Format("2006-01-02")
		}
		if err := tx.WithContext(ctx).Create(&row).Error; err != nil {
			return fmt.Errorf("apply review for card %d: %w", cardID, err)
		}
	}
	return nil
}

func parseTimePtr(s *string) *time.Time {
	if s == nil || *s == "" {
		return nil
	}
	t, err := time.Parse(time.RFC3339, *s)
	if err != nil {
		return nil
	}
	return &t
}

func errorEntries(errs []PackageImportError) []string {
	out := make([]string, 0, len(errs))
	for _, e := range errs {
		out = append(out, e.Entry+": "+e.Reason)
	}
	return out
}

// asPackageMediaForbidden 把 note 写入的 *MediaWriteError（写前校验拒绝）翻译成
// *PackageError，让包导入的消费者（REST/MCP/Web/CLI）用已有的 PackageError 分支映射
// 稳定 code 与逐条原因；其它错误原样返回（保持既有语义）。
func asPackageMediaForbidden(err error) error {
	var mwe *MediaWriteError
	if errors.As(err, &mwe) {
		return &PackageError{
			Code:    CodePackageMediaForbidden,
			Message: "the package references media you cannot read",
			Entries: mwe.Entries,
		}
	}
	return err
}
