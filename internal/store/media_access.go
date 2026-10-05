package store

import (
	"context"
	"errors"
	"fmt"

	"gorm.io/gorm"
)

// MediaAccessibleToUser 判定 sha256 为 mediaSha 的媒体是否可被 userID 读取（F2 的读取鉴权口径，
// DESIGN.md §6.3「鉴权只需一处（有卡组访问权的登录用户）」）。
//
// 允许读取的充要条件，二选一（判定本体是 mediaReadableByUser，读取与写入共用同一处定义）：
//  1. media_uploaders 里存在指向 userID 的记录——他提供过这份字节（去重命中也算，且永久有效）；
//  2. media_notes 映射里存在一条指向「userID 可见卡组内、未软删的 note」的记录。
//
// 不再扫 fields_json：映射是派生索引，由 note 写入路径维护，并用不变量测试锁住两者一致。
//
// F2c（已修复，2026-10-06）：共享卡组的 editor 曾能编辑别人写的 note、把他人媒体引用注入进去，
// 从而拿到读取权（映射建立即自我满足）。现在 note 写入前会对**写入之前的状态**校验本次新引入的
// 引用是否可读，越权引用在写入时即被拒（见 checkNewMediaRefs），故注入不再成立。
// 该修复依赖「所有 note 写入路径都经 NoteStore 的同一写入方法」，逐入口的越权注入用例守着它。
//
// 媒体不存在与无权限在调用方统一按 404 处理，这里只需回答「不可读」。
// 不做权限缓存：授权撤销必须在下一个请求即生效，缓存会把「撤销」变成「等失效」。
func MediaAccessibleToUser(ctx context.Context, db *gorm.DB, userID uint64, mediaSha string) (bool, error) {
	if db == nil || userID == 0 || mediaSha == "" {
		return false, nil
	}
	var m Media
	err := db.WithContext(ctx).Select("sha256").First(&m, "sha256 = ?", mediaSha).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("store: load media for access check: %w", err)
	}
	ok, err := mediaReadableByUser(ctx, db, userID, mediaSha)
	if err != nil {
		return false, err
	}
	return ok, nil
}
