package api

import (
	"context"
	"errors"
	"net/http"
	"testing"

	"git.nite07.com/nite/engram/internal/store"
)

// TestPreviewIntervals 覆盖四档间隔预览：按 Again/Hard/Good/Easy 排列，间隔逐档不减；
// 新卡的 Again 是预设的第一个学习步骤；共享卡组的成员按自己的预设预览。
func TestPreviewIntervals(t *testing.T) {
	env := newTestEnv(t, 600, 600)
	ctx := context.Background()
	owner := seedUser(t, env.db, "preview-owner", store.RoleUser)
	member := seedUser(t, env.db, "preview-member", store.RoleUser)
	deck := seedDeck(t, env.db, owner.ID)
	card := seedKindCard(t, env.db, deck.ID, "basic", `{"front":"q","back":"a"}`, "forward")
	if err := store.NewGrantStore(env.db).Grant(ctx, deck.ID, member.ID, store.RoleReader, &owner.ID); err != nil {
		t.Fatalf("Grant() error = %v", err)
	}
	// 属主的学习步骤是 2m；成员的默认预设关掉学习步骤，Again 不再是分钟级。
	if err := env.db.Model(&store.Preset{}).Where("owner_user_id = ?", owner.ID).Update("learning_steps", "2m").Error; err != nil {
		t.Fatalf("edit owner preset: %v", err)
	}
	if err := env.db.Model(&store.Preset{}).Where("owner_user_id = ?", member.ID).Update("learning_steps", "").Error; err != nil {
		t.Fatalf("edit member preset: %v", err)
	}

	cases := []struct {
		name       string
		user       *store.User
		wantAgainS int64
	}{
		{"owner uses 2m learning step", owner, 120},
		{"member without learning steps", member, -1},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := env.api.PreviewIntervals(ctx, tc.user, card.ID)
			if err != nil {
				t.Fatalf("PreviewIntervals() error = %v", err)
			}
			if len(got) != 4 {
				t.Fatalf("PreviewIntervals() returned %d entries, want 4", len(got))
			}
			for i, iv := range got {
				if iv.Rating != i+1 {
					t.Errorf("entry %d rating = %d, want %d", i, iv.Rating, i+1)
				}
				if i > 0 && iv.Seconds < got[i-1].Seconds {
					t.Errorf("rating %d interval %ds is shorter than rating %d interval %ds", iv.Rating, iv.Seconds, got[i-1].Rating, got[i-1].Seconds)
				}
			}
			if tc.wantAgainS >= 0 && got[0].Seconds != tc.wantAgainS {
				t.Errorf("Again interval = %ds, want %ds", got[0].Seconds, tc.wantAgainS)
			}
			if tc.wantAgainS < 0 && got[0].Seconds == 120 {
				t.Errorf("member preview used the owner's 2m learning step")
			}
		})
	}
}

// TestPreviewIntervalsNegatives 覆盖负例：非成员 forbidden，未知卡 not_found。
func TestPreviewIntervalsNegatives(t *testing.T) {
	env := newTestEnv(t, 600, 600)
	ctx := context.Background()
	owner := seedUser(t, env.db, "preview-neg-owner", store.RoleUser)
	stranger := seedUser(t, env.db, "preview-neg-stranger", store.RoleUser)
	deck := seedDeck(t, env.db, owner.ID)
	card := seedKindCard(t, env.db, deck.ID, "basic", `{"front":"q","back":"a"}`, "forward")

	cases := []struct {
		name       string
		user       *store.User
		cardID     uint64
		wantStatus int
	}{
		{"stranger", stranger, card.ID, http.StatusForbidden},
		{"unknown card", owner, card.ID + 1000, http.StatusNotFound},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := env.api.PreviewIntervals(ctx, tc.user, tc.cardID)
			var se *ServiceError
			if !errors.As(err, &se) || se.Status != tc.wantStatus {
				t.Fatalf("PreviewIntervals() error = %v, want status %d", err, tc.wantStatus)
			}
		})
	}
}
