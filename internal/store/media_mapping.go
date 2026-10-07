package store

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

// 媒体映射与可读性判定（-10-06 定）。
//
// 两个事实：
//   - media_notes：note 字段里引用了哪些媒体（派生索引，由 note 写入路径重建）；
//   - media_uploaders：谁提供过这份字节（上传与导入幂等登记，不可撤销）。
//
// 读取鉴权（read side）与写入前校验（write side）都从这两个事实推导，避免两处各写一套
// 「什么算可读」的判断而漂移。

// CodeMediaNotReadable 是 note 写入被写前校验拒绝时的稳定英文 code（条目级，见 MediaWriteError）。
// 它同时登记在 api 的英文错误码表里（internal/api 的 errorMessages，与 CodeMediaNotReadable 同值）。
// 错误文案按约定恒为英文、不落语言包，所以两份语言包里没有对应的 error.* 条目。
const CodeMediaNotReadable = "media_not_readable"

// MediaWriteError 表示一次 note 写入引用了写入者无法读取的媒体，整次写入被拒。
//
// Entries 逐条列出被拒的引用（形如 media/<sha256>），与卡组包的 PackageError.Entries 同构，
// 让调用方既能按 Code 程序化判断，也能把逐条原因展示给用户。
type MediaWriteError struct {
	Code    string
	Entries []string
}

func (e *MediaWriteError) Error() string {
	if len(e.Entries) == 0 {
		return e.Code
	}
	return e.Code + ": " + strings.Join(e.Entries, ", ")
}

// RecordMediaUploader 幂等登记「userID 提供过 sha 这份字节」。
//
// 上传与导入（凡本次提供字节的路径）都调用它；重复登记不报错、不建重复行（主键冲突忽略）。
// 写入后不可撤销：没有删除路径，删了「提供过」就不成立。
func RecordMediaUploader(ctx context.Context, db *gorm.DB, mediaSha string, userID uint64) error {
	if db == nil || mediaSha == "" || userID == 0 {
		return nil
	}
	row := MediaUploader{MediaSha: mediaSha, UserID: userID, CreatedAt: time.Now().UTC()}
	if err := db.WithContext(ctx).Clauses(clause.OnConflict{DoNothing: true}).Create(&row).Error; err != nil {
		return fmt.Errorf("store: record media uploader: %w", err)
	}
	return nil
}

// mediaRefsIn 返回字段映射里出现的全部媒体 sha256（URL 形态与包内形态都识别）。
// 复用 media_quota.go 的 scanMediaRefs，保证引用识别只有一处实现。
func mediaRefsIn(fields map[string]any) map[string]bool {
	refs := map[string]bool{}
	if fields == nil {
		return refs
	}
	scanMediaRefs(fields, refs)
	return refs
}

// mediaReadableByUser 判定 sha 的媒体是否可被 userID 读取（两支 + L3 的分享支）。
//
// 允许读取的充要条件，三选一：
//  1. media_uploaders 里存在指向 userID 的记录（他提供过这份字节，去重命中也算，且不可撤销）；
//  2. media_notes 映射里存在一条指向「userID 可见卡组内、未软删的 note」的记录；
//  3. shareSessionID 非空、且 media_notes 映射里存在一条指向「该**服务端会话**通过分享链接打开过、
//     且该授权未过期」的卡组内、未软删的 note（L3）。
//
// 第 3 支只对读取路径开放：写入前校验（checkNewMediaRefs）传空 sessionID。分享链接的访客是 reader，
// 本就不能写 note；把分享授权也算进「可读」，会让写前校验引用一个语义上不该有的来源。
//
// 它与读取端点 MediaAccessibleToUser 共用同一判定，也与写前校验共用——「可读」只有这一处定义。
func mediaReadableByUser(ctx context.Context, db *gorm.DB, userID uint64, mediaSha, shareSessionID string) (bool, error) {
	if db == nil || userID == 0 || mediaSha == "" {
		return false, nil
	}
	var uploads int64
	if err := db.WithContext(ctx).Table("(?) AS m", readableMediaUnionQuery(db, userID)).
		Where("m.sha256 = ?", mediaSha).
		Count(&uploads).Error; err != nil {
		return false, fmt.Errorf("store: count readable media: %w", err)
	}
	if uploads > 0 {
		return true, nil
	}
	// 第三支：本会话通过分享链接打开过的卡组（过期 / 已撤销链接由子查询排除，见 share_session.go）。
	if shareSessionID != "" {
		var shared int64
		if err := db.WithContext(ctx).Model(&MediaNote{}).
			Joins("JOIN notes ON notes.id = media_notes.note_id AND notes.deleted_at IS NULL").
			Where("media_notes.media_sha = ?", mediaSha).
			Where("notes.deck_id IN (?)", shareSessionDeckIDsQuery(db, shareSessionID, time.Now().UTC())).
			Count(&shared).Error; err != nil {
			return false, fmt.Errorf("store: count media share session refs: %w", err)
		}
		if shared > 0 {
			return true, nil
		}
	}
	return false, nil
}

// checkNewMediaRefs 在写入之前校验「本次新引入的引用」是否都是写入者（actor）可读的。
//
// 规则（「写入时校验」）：
//   - 只校验 newFields 里新出现、oldFields 里没有的 sha（旧引用本就已通过过校验，重复校验无意义）；
//   - 校验对**写入之前的状态**求值：调用方必须在写入媒体/映射之前调用它，否则映射一建立即自我满足；
//   - 库里根本没有这份字节（media 无行）的引用放行：它泄露不了任何东西，且是「导出关闭媒体后
//     再导入 / 引用已被删除的媒体」这类既有流程的正常形态；
//   - actor 为 0 表示服务端自身写入（无具名写入者），跳过校验。
//
// 返回 *MediaWriteError（条目级，稳定 code）以便调用方映射成 HTTP/MCP 错误。
func checkNewMediaRefs(ctx context.Context, db *gorm.DB, actor uint64, oldFields, newFields map[string]any) error {
	if actor == 0 {
		return nil
	}
	newRefs := mediaRefsIn(newFields)
	if len(newRefs) == 0 {
		return nil
	}
	oldRefs := mediaRefsIn(oldFields)
	var entries []string
	for sha := range newRefs {
		if oldRefs[sha] {
			continue
		}
		// 先确认库里确实有这份字节；没有则不构成泄露，放行。
		var m Media
		err := db.WithContext(ctx).Select("sha256").First(&m, "sha256 = ?", sha).Error
		if errors.Is(err, gorm.ErrRecordNotFound) {
			continue
		}
		if err != nil {
			return fmt.Errorf("store: check media ref %s: %w", sha, err)
		}
		ok, err := mediaReadableByUser(ctx, db, actor, sha, "")
		if err != nil {
			return err
		}
		if !ok {
			entries = append(entries, "media/"+sha)
		}
	}
	if len(entries) == 0 {
		return nil
	}
	sort.Strings(entries)
	return &MediaWriteError{Code: CodeMediaNotReadable, Entries: entries}
}

// rebuildMediaNotes 重建某 note 的 media_notes 映射：删掉该 note 的全部旧行，再按新字段插入。
//
// 必须**重建**而非追加：追加会让「删掉引用」不再撤销读取权——旧行留在表里，
// 读取鉴权仍能命中它。它跑在同一个事务里，与 note 写入同生共死。
func rebuildMediaNotes(ctx context.Context, tx *gorm.DB, noteID uint64, fields map[string]any) error {
	if err := tx.WithContext(ctx).Where("note_id = ?", noteID).Delete(&MediaNote{}).Error; err != nil {
		return fmt.Errorf("store: clear media notes for note %d: %w", noteID, err)
	}
	refs := mediaRefsIn(fields)
	if len(refs) == 0 {
		return nil
	}
	now := time.Now().UTC()
	rows := make([]MediaNote, 0, len(refs))
	for sha := range refs {
		rows = append(rows, MediaNote{MediaSha: sha, NoteID: noteID, CreatedAt: now})
	}
	if err := tx.WithContext(ctx).Clauses(clause.OnConflict{DoNothing: true}).Create(&rows).Error; err != nil {
		return fmt.Errorf("store: insert media notes for note %d: %w", noteID, err)
	}
	return nil
}
