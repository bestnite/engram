package web

import (
	"context"
)

// This file gathers the "numeric primary key -> public id" reverse-lookup helpers.
//
// Outlets such as audit detail and mail variables carry no model directly, only primary keys; they also end up in responses (admin panel audit,
// mail bodies), so the primary keys are translated into public ids here. A failed lookup (row deleted or not wired up) always returns an empty string,
// never falling back to the numeric primary key — otherwise the opaque-id boundary would leak through these side channels.

// userPublicID reverse-looks-up a user's public id; empty string when not found.
func (s *Server) userPublicID(ctx context.Context, id uint64) string {
	if s.users == nil || id == 0 {
		return ""
	}
	u, err := s.users.ByID(ctx, id)
	if err != nil || u == nil {
		return ""
	}
	return u.PublicID
}

// presetPublicID reverse-looks-up a preset's public id; empty string when not found.
func (s *Server) presetPublicID(ctx context.Context, id uint64) string {
	if s.presets == nil || id == 0 {
		return ""
	}
	p, err := s.presets.ByID(ctx, id)
	if err != nil || p == nil {
		return ""
	}
	return p.PublicID
}

// deckPublicID reverse-looks-up a deck's public id; empty string when not found.
func (s *Server) deckPublicID(ctx context.Context, id uint64) string {
	if s.decks == nil || id == 0 {
		return ""
	}
	d, err := s.decks.ByID(ctx, id)
	if err != nil || d == nil {
		return ""
	}
	return d.PublicID
}

// looksLikePublicID reports whether a string looks like a public id (the 36-character UUIDv7 text: 8-4-4-4-12 hex).
// It only checks the shape and never queries: a badly shaped parameter can be refused before the query, so no arbitrary string gets to probe the index.
func looksLikePublicID(raw string) bool {
	if len(raw) != 36 {
		return false
	}
	for i, r := range raw {
		switch i {
		case 8, 13, 18, 23:
			if r != '-' {
				return false
			}
		default:
			if !((r >= '0' && r <= '9') || (r >= 'a' && r <= 'f') || (r >= 'A' && r <= 'F')) {
				return false
			}
		}
	}
	return true
}
