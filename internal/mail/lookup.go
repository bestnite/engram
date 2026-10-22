package mail

import (
	"context"
	"errors"
	"log/slog"

	"git.nite07.com/nite/engram/internal/store"
)

// StoreLookup builds a lookup function from the template store, shared by the web layer and the
// periodic worker.
//
// Sharing is deliberate: querying the DB, deciding "no customization", and logging only on a real
// failure -- writing that logic twice would inevitably drift, and the drift shows up as a
// hard-to-notice inconsistency like "mail sent from the web has a template, mail sent from the worker
// does not".
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
			// "This key has no customization" is a normal state and is not logged; only a real DB failure
			// is.
			if !errors.Is(err, store.ErrMailTemplateNotFound) {
				log.Error("load mail template failed", "mail_type", string(t), "locale", locale, "error", err)
			}
			return "", "", false
		}
		return row.Subject, row.BodyMD, true
	}
}
