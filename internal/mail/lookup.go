package mail

import (
	"context"
	"errors"
	"log/slog"

	"git.nite07.com/nite/engram/internal/store"
)

// StoreLookup 用模板存储构造查找函数，供 web 层与周期 worker 共用。
//
// 共用是刻意的：查库、判「没有自定义」、只在真失败时记日志——这套逻辑两处各写一份
// 必然漂移，而漂移的后果是「web 发的信有模板、worker 发的没有」这种很难察觉的不一致。
func StoreLookup(st *store.MailTemplateStore, log *slog.Logger) LookupFunc {
	if st == nil {
		return nil
	}
	if log == nil {
		log = slog.Default()
	}
	return func(t Type, locale string) (string, string, bool) {
		row, err := st.ByTypeLocale(context.Background(), string(t), locale)
		if err != nil {
			// 「这条键没有自定义」是正常状态，不记日志；真正的查库失败才记。
			if !errors.Is(err, store.ErrMailTemplateNotFound) {
				log.Error("load mail template failed", "mail_type", string(t), "locale", locale, "error", err)
			}
			return "", "", false
		}
		return row.Subject, row.BodyMD, true
	}
}
