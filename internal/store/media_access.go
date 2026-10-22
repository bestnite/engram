package store

import (
	"context"
	"errors"
	"fmt"

	"gorm.io/gorm"
)

// MediaAccessibleToUser decides whether the media whose sha256 is mediaSha may be read by userID (the read authorization rule).
//
// sessionID is the server-side session id that made the request (L3): when non-empty it additionally allows references in decks that session
// opened through a share link and whose grant has not expired. The session id is taken from the request context by the web layer and passed explicitly, not as a package global and
// not stuffed into context — the inputs to read authorization must be visible in this function's signature, since hidden state makes 'who read what' unauditable.
// Anonymous requests (not logged in) never get here at all (mediaServe requires login first), so an empty string is fine.
//
// The necessary and sufficient conditions for allowing a read, one of three (the decision itself is mediaReadableByUser, shared by reads and writes):
//  1. media_uploaders has a record pointing at userID — they supplied these bytes (a dedup hit counts too, and it is valid forever);
//  2. the media_notes mapping has a record pointing at 'a non-soft-deleted note inside a deck visible to userID';
//  3. the media_notes mapping has a record pointing at 'a non-soft-deleted note inside a deck this session opened via a share link whose grant is unexpired'.
//
// fields_json is no longer scanned: the mapping is a derived index maintained by the note write path, with an invariant test locking the two together.
//
// F2c (fixed, 2026-10-06): an editor of a shared deck used to be able to edit a note someone else wrote and inject their media references into it,
// thereby gaining read access (creating the mapping satisfied itself). Now, before a note is written, the newly introduced references are
// validated for readability against the **state before the write**, so an unauthorized reference is rejected at write time (see checkNewMediaRefs) and the injection no longer works.
// That fix relies on 'every note write path goes through the same NoteStore write method', guarded by per-entry-point privilege-injection cases.
//
// A missing medium and a lack of permission are both mapped to 404 by the caller; here it only has to answer 'not readable'.
// No permission caching: revoking a grant must take effect on the next request, caching would turn 'revoke' into 'wait for expiry'.
func MediaAccessibleToUser(ctx context.Context, db *gorm.DB, userID uint64, sessionID, mediaSha string) (bool, error) {
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
	ok, err := mediaReadableByUser(ctx, db, userID, mediaSha, sessionID)
	if err != nil {
		return false, err
	}
	return ok, nil
}
