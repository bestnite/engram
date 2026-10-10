package store

import (
	"context"
	"reflect"
	"sort"
	"testing"
	"time"
)

// TestWhereNoteTagsAny 断言标签匹配按数组元素精确命中（大小写不敏感），不做前缀或子串匹配，
// 且含引号、反斜杠、<>& 与 LIKE 通配符的标签也能命中——这些字符在 tags_json 里以 JSON 转义形式存放。
func TestWhereNoteTagsAny(t *testing.T) {
	tagSets := map[string][]string{
		"plain":   {"文化传承"},
		"prefix":  {"文化传承类"},
		"two":     {"创新", "高频"},
		"quote":   {`say "hi"`},
		"slash":   {`a\b`},
		"html":    {"<x>&y"},
		"percent": {"50%_off"},
		"upper":   {"Go"},
		"none":    {},
	}
	cases := []struct {
		name string
		tags []string
		want []string
	}{
		{"exact element only", []string{"文化传承"}, []string{"plain"}},
		{"union of tags", []string{"文化传承", "创新"}, []string{"plain", "two"}},
		{"quote is escaped in storage", []string{`say "hi"`}, []string{"quote"}},
		{"backslash is escaped in storage", []string{`a\b`}, []string{"slash"}},
		{"html chars are escaped in storage", []string{"<x>&y"}, []string{"html"}},
		{"like wildcards are literal", []string{"50%_off"}, []string{"percent"}},
		{"wildcard does not match others", []string{"%"}, nil},
		{"case insensitive", []string{"go"}, []string{"upper"}},
		{"substring does not match", []string{"高"}, nil},
	}
	for driver, db := range testDatabases(t) {
		t.Run(driver, func(t *testing.T) {
			if err := db.AutoMigrate(AllModels()...); err != nil {
				t.Fatalf("AutoMigrate() error = %v", err)
			}
			owner := seedUsers(t, db, "tags_any_owner")[0]
			deckID := seedDeck(t, db, owner)
			now := time.Now().UTC()
			ids := map[uint64]string{}
			for name, tags := range tagSets {
				n := Note{DeckID: deckID, Kind: "basic", FieldsJSON: `{"front":"q","back":"a"}`,
					TagsJSON: TagsJSON(tags), CreatedAt: now, UpdatedAt: now}
				if err := db.Create(&n).Error; err != nil {
					t.Fatalf("create note: %v", err)
				}
				ids[n.ID] = name
			}
			for _, tc := range cases {
				t.Run(tc.name, func(t *testing.T) {
					var got []uint64
					q := WhereNoteTagsAny(db.Model(&Note{}).Where("notes.deck_id = ?", deckID), tc.tags)
					if err := q.Pluck("notes.id", &got).Error; err != nil {
						t.Fatalf("query: %v", err)
					}
					names := make([]string, 0, len(got))
					for _, id := range got {
						names = append(names, ids[id])
					}
					sort.Strings(names)
					want := append([]string{}, tc.want...)
					sort.Strings(want)
					if len(names) != len(want) || (len(want) > 0 && !reflect.DeepEqual(names, want)) {
						t.Fatalf("WhereNoteTagsAny(%q) matched %v, want %v", tc.tags, names, want)
					}
				})
			}
		})
	}
}

// TestDeckTagCounts 断言卡组标签汇总：只算本卡组未删除的 note，同一 note 的重复标签只计一次，
// 坏数据按无标签处理，结果按标签名升序。
func TestDeckTagCounts(t *testing.T) {
	for driver, db := range testDatabases(t) {
		t.Run(driver, func(t *testing.T) {
			if err := db.AutoMigrate(AllModels()...); err != nil {
				t.Fatalf("AutoMigrate() error = %v", err)
			}
			ctx := context.Background()
			owner := seedUsers(t, db, "deck_tag_counts_owner")[0]
			deckID := seedDeck(t, db, owner)
			otherDeckID := seedDeck(t, db, owner)
			now := time.Now().UTC()
			mk := func(deck uint64, raw string) Note {
				t.Helper()
				n := Note{DeckID: deck, Kind: "basic", FieldsJSON: `{"front":"q","back":"a"}`,
					TagsJSON: raw, CreatedAt: now, UpdatedAt: now}
				if err := db.Create(&n).Error; err != nil {
					t.Fatalf("create note: %v", err)
				}
				return n
			}
			mk(deckID, `["创新","文化传承"]`)
			mk(deckID, `["创新","创新"]`)
			mk(deckID, `[]`)
			mk(deckID, `not json`)
			deleted := mk(deckID, `["已删除"]`)
			if err := db.Delete(&deleted).Error; err != nil {
				t.Fatalf("soft delete note: %v", err)
			}
			mk(otherDeckID, `["别的卡组"]`)

			got, err := NewNoteStore(db).DeckTagCounts(ctx, deckID)
			if err != nil {
				t.Fatalf("DeckTagCounts() error = %v", err)
			}
			want := []TagCount{{Tag: "创新", Notes: 2}, {Tag: "文化传承", Notes: 1}}
			if !reflect.DeepEqual(got, want) {
				t.Fatalf("DeckTagCounts() = %+v, want %+v", got, want)
			}

			empty, err := NewNoteStore(db).DeckTagCounts(ctx, 999999)
			if err != nil {
				t.Fatalf("DeckTagCounts(missing) error = %v", err)
			}
			if empty == nil || len(empty) != 0 {
				t.Fatalf("DeckTagCounts(missing) = %#v, want empty non-nil slice", empty)
			}
		})
	}
}
