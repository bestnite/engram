package store

import (
	"context"
	"strings"
	"testing"
	"time"
)

// TestDeleteUserCleansEverythingAndKeepsSharedMedia 建一个与别人有各种交集的用户再删除它，断言：
//   - 任何表的用户列里都不再出现它的 id（删除或置空）；
//   - 它名下卡组的级联与卡组删除一致：别人在这些卡组上的授权与进度一起消失；
//   - 它上传、但被别人 note 引用的媒体行保留（媒体按内容去重共享，删行会让别人的图片失效）；
//   - 它在别人卡组里写的 note 保留，created_by 置空。
func TestDeleteUserCleansEverythingAndKeepsSharedMedia(t *testing.T) {
	for driver, db := range testDatabases(t) {
		t.Run(driver, func(t *testing.T) {
			ctx := context.Background()
			if err := AutoMigrate(ctx, db); err != nil {
				t.Fatalf("AutoMigrate() error = %v", err)
			}
			now := time.Now().UTC()
			ids := seedUsers(t, db, "del_victim", "del_owner", "del_reader")
			victim, owner, reader := ids[0], ids[1], ids[2]
			mustCreate := func(v any) {
				t.Helper()
				if err := db.Create(v).Error; err != nil {
					t.Fatalf("seed %T: %v", v, err)
				}
			}

			// 受害者自己的卡组，被 reader 共享并学习过。
			victimDeck := seedDeck(t, db, victim)
			ownNote := Note{DeckID: victimDeck, Kind: "basic", FieldsJSON: `{"front":"q","back":"a"}`, TagsJSON: "[]", CreatedBy: &victim, CreatedAt: now, UpdatedAt: now}
			mustCreate(&ownNote)
			ownCard := Card{NoteID: ownNote.ID, Template: "forward", CreatedAt: now}
			mustCreate(&ownCard)
			mustCreate(&DeckGrant{DeckID: victimDeck, UserID: reader, Role: RoleReader, CreatedBy: &victim, CreatedAt: now})
			mustCreate(&CardState{CardID: ownCard.ID, UserID: reader, State: "review", Version: 1})

			// owner 的卡组里，受害者作为编辑写了一条引用自己上传媒体的 note。
			ownerDeck := seedDeck(t, db, owner)
			sha := strings.Repeat("d", 64)
			mustCreate(&Media{Sha256: sha, RelPath: "dd/" + sha + ".png", Mime: "image/png", Bytes: 10, CreatedBy: &victim, CreatedAt: now})
			if err := RecordMediaUploader(ctx, db, sha, victim); err != nil {
				t.Fatalf("record uploader: %v", err)
			}
			guestNote := Note{DeckID: ownerDeck, Kind: "basic", FieldsJSON: `{"front":"![](/media/` + sha + `)","back":"a"}`, TagsJSON: "[]", CreatedBy: &victim, CreatedAt: now, UpdatedAt: now}
			mustCreate(&guestNote)
			mustCreate(&MediaNote{MediaSha: sha, NoteID: guestNote.ID, CreatedAt: now})
			mustCreate(&DeckGrant{DeckID: ownerDeck, UserID: victim, Role: RoleEditor, CreatedBy: &owner, CreatedAt: now})

			// 个人数据。
			mustCreate(&DeckShareInvite{DeckID: ownerDeck, UserID: reader, Role: RoleReader, InvitedBy: victim, CreatedAt: now, ExpiresAt: now.Add(time.Hour)})
			mustCreate(&ShareAllow{FromUserID: owner, ToUserID: victim, CreatedAt: now})
			mustCreate(&UserTOTP{UserID: victim, SecretCiphertext: "v1:x", CreatedAt: now, UpdatedAt: now})
			mustCreate(&TOTPRecoveryCode{UserID: victim, CodeHash: "h", CreatedAt: now})
			mustCreate(&EmailPref{UserID: victim, ChoicesJSON: "{}", UpdatedAt: now})
			mustCreate(&LoginFingerprint{UserID: victim, Fingerprint: "fp", FirstSeenAt: now, LastSeenAt: now})
			mustCreate(&Invite{Token: "tok", Role: RoleUser, CreatedBy: &victim, UsedBy: &victim, CreatedAt: now})

			if err := NewUserStore(db).DeleteUser(ctx, victim); err != nil {
				t.Fatalf("DeleteUser() error = %v", err)
			}

			userColumns := map[string][]string{
				"users": {"id"}, "sessions": {"user_id"}, "identities": {"user_id"}, "api_keys": {"user_id"},
				"card_states": {"user_id"}, "reviews": {"user_id"}, "deck_grants": {"user_id", "created_by"},
				"deck_share_invites": {"user_id", "invited_by"}, "share_allow": {"from_user_id", "to_user_id"},
				"share_links": {"created_by"}, "user_totp": {"user_id"}, "totp_recovery_codes": {"user_id"},
				"email_prefs": {"user_id"}, "reminder_log": {"user_id"}, "digest_log": {"user_id"},
				"action_tokens": {"user_id"}, "login_fingerprints": {"user_id"}, "media_uploaders": {"user_id"},
				"notes": {"created_by"}, "invites": {"created_by", "used_by"}, "media": {"created_by"},
				"settings": {"updated_by"}, "mail_templates": {"updated_by"}, "decks": {"owner_user_id"},
				"presets": {"owner_user_id"},
			}
			for table, cols := range userColumns {
				for _, col := range cols {
					var n int64
					if err := db.Table(table).Where(col+" = ?", victim).Count(&n).Error; err != nil {
						t.Fatalf("count %s.%s: %v", table, col, err)
					}
					if n != 0 {
						t.Errorf("%s.%s still references the deleted user in %d rows", table, col, n)
					}
				}
			}

			checks := []struct {
				name  string
				table string
				where string
				arg   any
				want  int64
			}{
				{"reader's grant on the deleted user's deck is gone", "deck_grants", "deck_id = ?", victimDeck, 0},
				{"reader's progress on the deleted user's cards is gone", "card_states", "card_id = ?", ownCard.ID, 0},
				{"shared media row is kept", "media", "sha256 = ?", sha, 1},
				{"note written in someone else's deck is kept", "notes", "id = ?", guestNote.ID, 1},
				{"its media mapping is kept", "media_notes", "note_id = ?", guestNote.ID, 1},
			}
			for _, c := range checks {
				var n int64
				if err := db.Table(c.table).Where(c.where, c.arg).Count(&n).Error; err != nil {
					t.Fatalf("%s: %v", c.name, err)
				}
				if n != c.want {
					t.Errorf("%s: %d rows, want %d", c.name, n, c.want)
				}
			}
		})
	}
}
