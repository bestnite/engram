package store

import (
	"context"
	"errors"
	"fmt"

	"gorm.io/gorm"
)

// mediaAccessScanBatch 是扫描引用时的分批大小：每次只取这么多条 note 的字段进内存。
// 可见卡组里的 note 可能很多，字段又是整段 fields_json，逐批 keyset 翻页把内存占用约束成
// 一个批次的规模，而不是随卡组规模增长。
const mediaAccessScanBatch = 500

// MediaAccessibleToUser 判定 sha256 为 mediaSha 的媒体是否可被 userID 读取（F2 的读取鉴权口径，
// DESIGN.md §6.3「鉴权只需一处（有卡组访问权的登录用户）」）。
//
// 允许读取的充要条件，二选一：
//  1. 当前用户可见卡组内、未被软删除、**且不是当前用户自己写的** note 的字段里精确引用了它
//     （编辑器写入的 `/media/<sha256>`，或包内形态 `media/<sha256>.<ext>`）；
//  2. 或该 media 的 created_by = 当前用户——覆盖「刚上传、尚未插入任何卡片」的编辑器预览；
//     媒体按 sha256 内容寻址，去重后字节相同，上传者读自己上传的文件不构成对他人的泄露。
//
// 为什么口径①要排除「自己写的 note」（F2b）：note 的字段由用户自己可写，若「自己写的引用」也算
// 授权，攻击者只要在自己卡组里写一张 `![]("/media/<目标 sha>")` 的卡，该 media 就落入「他可见卡组内
// 被引用」而放行；一张卡能写很多 sha，于是按 sha 逐个枚举他人媒体仍然可行。把引用来源限定为
// 「别人写的 note」后，攻击者无法用自己可写的文本为自己开权限。
//
// created_by IS NULL 视为「不是我写的」而放行：NULL 只出现在服务端自身写入的行（导入、系统生成），
// 攻击者无法通过网页/接口把自己的 note 的 created_by 写成 NULL，因此它不是攻击者可控制的输入。
//
// 为什么扫字段而不是查关联表：media 与 note 之间没有关联表，引用只是 note.fields_json 里的
// 字符串（与 UserMediaUsage 同一事实）。为它建表意味着回填历史数据、并在每次编辑字段时同步，
// 任何一处漏写就会把「能读的读不了 / 不能读的读得了」变成两个方向的 bug；保持单一是更小的
// 风险面，代价只是这里扫一遍可见 note 的字段。
//
// 复用而不是复制：
//   - 可见卡组谓词来自 visibleDeckIDsQuery（与列表、队列、统计同源，允许的卡组集合一致）；
//   - note 的软删由 GORM 默认作用域排除，删 note 即失去这条读取权；
//   - 引用的识别复用 media_quota.go 的 scanMediaRefs。它只捕获完整的 64 位小写十六进制段，
//     因此不存在前缀误配。
//
// 已知残留（本函数不修）：共享卡组的 editor 能编辑别人写的 note，而 NoteStore.Update 不改
// note.created_by。于是 editor 可以把自己的引用注入到「不是他写的」note 里，重新拿到读取权。
// 记录在 media_access_test.go 的 TestMediaAccessResidualEditorInjectionStillGrants 里。
//
// 不做权限缓存：授权撤销必须在下一个请求即生效，缓存会把「撤销」变成「等失效」。
func MediaAccessibleToUser(ctx context.Context, db *gorm.DB, userID uint64, mediaSha string) (bool, error) {
	if db == nil || userID == 0 || mediaSha == "" {
		return false, nil
	}
	var m Media
	err := db.WithContext(ctx).Select("sha256", "created_by").First(&m, "sha256 = ?", mediaSha).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		// 媒体不存在与无权限在调用方统一按 404 处理，这里只需回答「不可读」。
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("store: load media for access check: %w", err)
	}
	if m.CreatedBy != nil && *m.CreatedBy == userID {
		return true, nil
	}

	shas := map[string]bool{}
	lastID := uint64(0)
	for {
		var notes []Note
		if err := db.WithContext(ctx).
			Select("id", "fields_json").
			Model(&Note{}).
			Where("deck_id IN (?)", visibleDeckIDsQuery(db, userID)).
			// F2b：引用必须是「别人/系统写的」，排除当前用户自己写的 note（NULL 视为不是我写的）。
			// 条件放在 SQL 里而不是内存循环里，才能让分页每批都少读不该授权的行。
			Where("(created_by IS NULL OR created_by <> ?)", userID).
			Where("id > ?", lastID).
			Order("id ASC").
			Limit(mediaAccessScanBatch).
			Find(&notes).Error; err != nil {
			return false, fmt.Errorf("store: list notes for media access: %w", err)
		}
		if len(notes) == 0 {
			return false, nil
		}
		for i := range notes {
			fields, perr := ParseFields(notes[i].FieldsJSON)
			if perr != nil {
				// 坏字段不阻断鉴权：跳过该 note，既不因此放行也不因此报错。
				continue
			}
			scanMediaRefs(fields, shas)
			if shas[mediaSha] {
				return true, nil
			}
		}
		lastID = notes[len(notes)-1].ID
	}
}
