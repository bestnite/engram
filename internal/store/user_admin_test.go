package store

import (
	"context"
	"sort"
	"testing"
	"time"
)

// TestListForAdminSearch pins the admin user search: a substring match over username, email and display name,
// case-insensitive, with LIKE wildcards in the query matching only themselves.
func TestListForAdminSearch(t *testing.T) {
	for driver, db := range testDatabases(t) {
		t.Run(driver, func(t *testing.T) {
			if err := db.AutoMigrate(AllModels()...); err != nil {
				t.Fatalf("AutoMigrate() error = %v", err)
			}
			ctx := context.Background()
			users := NewUserStore(db)
			seed := []struct{ username, email, display string }{
				{"alice", "alice@example.com", "Alice Liddell"},
				{"bob_smith", "bob@example.com", "Bob"},
				{"carol", "carol@example.org", "Carol 100% Sure"},
				{"dave", "dave@example.net", "Dave"},
			}
			for _, s := range seed {
				u := &User{Username: s.username, Email: s.email, DisplayName: s.display,
					Role: RoleUser, Status: StatusActive, Locale: "en", Timezone: "UTC", CreatedAt: time.Now().UTC()}
				if err := users.Create(ctx, u); err != nil {
					t.Fatalf("Create(%s) error = %v", s.username, err)
				}
			}

			cases := []struct {
				name  string
				query string
				want  []string
			}{
				{"empty query returns everyone", "", []string{"alice", "bob_smith", "carol", "dave"}},
				{"underscore matches only an underscore", "_", []string{"bob_smith"}},
				{"percent matches only a percent sign", "%", []string{"carol"}},
				{"substring is case-insensitive", "LIDDELL", []string{"alice"}},
				{"email substring", "example.org", []string{"carol"}},
				{"underscore inside a term stays literal", "b_s", []string{"bob_smith"}},
				{"no match", "zed", nil},
			}
			for _, tc := range cases {
				t.Run(tc.name, func(t *testing.T) {
					got, total, err := users.ListForAdmin(ctx, tc.query, 1, AdminListUsersPageSize)
					if err != nil {
						t.Fatalf("ListForAdmin(%q) error = %v", tc.query, err)
					}
					names := make([]string, 0, len(got))
					for _, u := range got {
						names = append(names, u.Username)
					}
					sort.Strings(names)
					if len(names) != len(tc.want) || int(total) != len(tc.want) {
						t.Fatalf("ListForAdmin(%q) = %v (total %d), want %v", tc.query, names, total, tc.want)
					}
					for i := range names {
						if names[i] != tc.want[i] {
							t.Fatalf("ListForAdmin(%q) = %v, want %v", tc.query, names, tc.want)
						}
					}
				})
			}
		})
	}
}
