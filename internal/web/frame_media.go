package web

import (
	"context"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"fmt"
	"net/http"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"gorm.io/gorm"

	"git.nite07.com/nite/engram/internal/media"
	"git.nite07.com/nite/engram/internal/store"
)

// Frame media: the card frame loads its media through /fm/<token>/<name> instead of the session-bound /media/<sha256> route.
//
// The frame document is sandboxed with an opaque origin and carries no session cookie, so a plain /media/ URL would be
// answered with 401. A token therefore stands in for the session: it is an HMAC over (user_id, deck_id, expiry) signed with
// a key the server keeps in the encrypted settings channel. Every request checks the signature, the expiry and the user's
// current read access to the deck, so revoking a grant takes effect on the next request rather than at token expiry.
const (
	// frameMediaPath is the frame media route. <name> is a deck-scoped media name resolved through media_names.
	frameMediaPath = "/fm/:token/:name"
	// frameMediaKeySetting is the settings key holding the HMAC key. The name ends in _key, so the settings layer treats it
	// as sensitive and stores it encrypted; the plaintext never leaves the process.
	frameMediaKeySetting = "frame_media_key"
	// frameMediaKeyBytes is the key length: 32 random bytes are plenty for one HMAC use and match AES-256 keys elsewhere.
	frameMediaKeyBytes = 32
	// frameMediaTokenTTL is how long one issued token stays valid. Six hours covers a review session without keeping a
	// leaked URL useful for days.
	frameMediaTokenTTL = 6 * time.Hour
)

// errFrameMediaUnavailable means the token feature cannot work: either the encrypted settings channel is not wired, or the
// database/media store needed to verify a request is missing. Callers degrade to an empty media base and an empty 404.
var errFrameMediaUnavailable = errors.New("frame media: secrets are not wired")

// frameMediaClaims is the token payload: which user may read which deck's media until when.
type frameMediaClaims struct {
	userID uint64
	deckID uint64
	expiry time.Time
}

// registerFrameMediaRoutes mounts the frame media route. It is registered unconditionally: the handler answers 404 when
// the dependencies are missing, which keeps the degradation identical to every other rejection (no signal about why).
func (s *Server) registerFrameMediaRoutes(router *gin.Engine) {
	router.GET(frameMediaPath, s.frameMediaServe)
}

// frameMediaServe serves one frame media read.
//
// All checks map to the same bare 404: a bad signature, an expired token, a tampered payload, a missing or inactive user,
// a deck the user cannot read, an unknown media name and an unreadable medium are indistinguishable to the caller. Only the
// debug log records which check failed; the token itself is never logged, because it is a bearer credential for six hours.
func (s *Server) frameMediaServe(c *gin.Context) {
	if s.db == nil || s.secrets == nil || s.media == nil || s.users == nil || s.access == nil {
		s.frameMediaReject(c, "frame media dependencies are not wired")
		return
	}
	ctx := c.Request.Context()
	claims, ok := s.verifyFrameMediaToken(ctx, c.Param("token"), time.Now())
	if !ok {
		s.frameMediaReject(c, "token rejected")
		return
	}
	user, err := s.users.ByID(ctx, claims.userID)
	if err != nil || user.Status != store.StatusActive {
		s.frameMediaReject(c, "token user is missing or inactive")
		return
	}
	// Deck access is re-checked on every request from the shared auth predicate: a revoked grant must fail immediately,
	// which a cached decision or a token that merely proved the access at issue time would not do.
	if _, _, err := s.access.RequireRole(ctx, claims.deckID, user.ID, store.RoleReader); err != nil {
		s.frameMediaReject(c, "token user cannot read the token deck")
		return
	}
	// Authorization is deck read access plus a media_names row for this deck. The row is what makes the name belong to
	// the deck, so a CSS-only asset such as "_font.woff2" is served without needing a note that references it.
	name, err := store.NewMediaNameStore(s.db).ByName(ctx, claims.deckID, c.Param("name"))
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			s.frameMediaReject(c, "media name is unknown in the token deck")
			return
		}
		s.frameMediaReject(c, "media name lookup failed")
		return
	}
	m, f, err := s.media.Open(ctx, name.Sha256)
	if err != nil {
		if errors.Is(err, media.ErrNotFound) {
			s.frameMediaReject(c, "media is missing from storage")
			return
		}
		s.frameMediaReject(c, "open media failed")
		return
	}
	defer f.Close()

	// The frame is embedded on the site itself; a wildcard origin keeps the response usable from the sandboxed, opaque-origin
	// frame while Cache-Control private stops shared caches from storing a token-scoped response.
	c.Header("Access-Control-Allow-Origin", "*")
	c.Header("Cache-Control", "private")
	writeMediaHeaders(c, m)
	if c.GetHeader("If-None-Match") == `"`+m.Sha256+`"` {
		c.Status(http.StatusNotModified)
		return
	}
	// ServeContent keeps range requests working, which media playback in the frame needs.
	http.ServeContent(c.Writer, c.Request, "", m.CreatedAt, f)
}

// frameMediaReject answers a failed frame media request: a bare 404 with an empty body and no cache header, plus one debug
// line naming the failed check. The response carries nothing that would let a caller learn which check rejected it.
func (s *Server) frameMediaReject(c *gin.Context, reason string) {
	s.logger.Debug("frame media request rejected", "reason", reason)
	c.AbortWithStatus(http.StatusNotFound)
}

// frameMediaBase is the <base href> prefix for the frame document: "/fm/<token>/" for one user and one deck, or "" when the
// feature is unavailable. An empty value means callers must leave the card HTML untouched — without a base, a relative
// media name would resolve against the current page instead of the token route.
func (s *Server) frameMediaBase(ctx context.Context, userID, deckID uint64) string {
	token, err := s.frameMediaToken(ctx, userID, deckID, time.Now())
	if err != nil {
		if !errors.Is(err, errFrameMediaUnavailable) {
			s.logger.Error("frame media token issue failed", "user_id", userID, "deck_id", deckID, "error", err)
		}
		return ""
	}
	return "/fm/" + token + "/"
}

// frameMediaToken signs a claim set. The token is
//
//	base64url(payload) "." base64url(HMAC-SHA256(key, base64url(payload)))
//
// with payload = "<user_id>.<deck_id>.<expiry unix seconds>". The signing input is the encoded payload string, so
// verification never has to re-encode anything before comparing; both segments use unpadded base64url, which is
// path-safe.
func (s *Server) frameMediaToken(ctx context.Context, userID, deckID uint64, now time.Time) (string, error) {
	if s.secrets == nil || s.db == nil {
		return "", errFrameMediaUnavailable
	}
	key, err := s.frameMediaSigningKey(ctx)
	if err != nil {
		return "", err
	}
	claims := frameMediaClaims{userID: userID, deckID: deckID, expiry: now.Add(frameMediaTokenTTL).UTC()}
	payload := strconv.FormatUint(claims.userID, 10) + "." +
		strconv.FormatUint(claims.deckID, 10) + "." +
		strconv.FormatInt(claims.expiry.Unix(), 10)
	encoded := base64.RawURLEncoding.EncodeToString([]byte(payload))
	return encoded + "." + base64.RawURLEncoding.EncodeToString(frameMediaMAC(key, encoded)), nil
}

// verifyFrameMediaToken checks a token's shape, signature, payload and expiry. Every failure returns false and no
// distinction: the handler maps the whole result to one 404.
func (s *Server) verifyFrameMediaToken(ctx context.Context, token string, now time.Time) (frameMediaClaims, bool) {
	if s.secrets == nil || s.db == nil {
		return frameMediaClaims{}, false
	}
	encoded, signature, ok := strings.Cut(token, ".")
	// strings.Cut splits at the first dot only; a second dot means trailing data, which is rejected here.
	if !ok || encoded == "" || strings.Contains(signature, ".") {
		return frameMediaClaims{}, false
	}
	key, err := s.frameMediaSigningKey(ctx)
	if err != nil {
		s.logger.Debug("frame media token key unavailable", "reason", err.Error())
		return frameMediaClaims{}, false
	}
	rawSignature, err := base64.RawURLEncoding.Strict().DecodeString(signature)
	if err != nil {
		return frameMediaClaims{}, false
	}
	// Constant-time comparison: a byte-by-byte equality check would leak how much of a forged signature was right.
	if !hmac.Equal(rawSignature, frameMediaMAC(key, encoded)) {
		return frameMediaClaims{}, false
	}
	rawPayload, err := base64.RawURLEncoding.Strict().DecodeString(encoded)
	if err != nil {
		return frameMediaClaims{}, false
	}
	claims, ok := parseFrameMediaPayload(string(rawPayload))
	if !ok {
		return frameMediaClaims{}, false
	}
	if !now.Before(claims.expiry) {
		return frameMediaClaims{}, false
	}
	return claims, true
}

// frameMediaMAC computes the signature over the encoded payload segment.
func frameMediaMAC(key []byte, encodedPayload string) []byte {
	mac := hmac.New(sha256.New, key)
	mac.Write([]byte(encodedPayload))
	return mac.Sum(nil)
}

// parseFrameMediaPayload parses "<user_id>.<deck_id>.<expiry>" strictly. Each part must be the canonical decimal spelling
// of its value, so a payload cannot be respelled into a second encoding that verifies with the same signature ("01",
// "+1", "-0" and the like are all refused). Zero user, deck or expiry is refused too: no legitimate claim carries them.
func parseFrameMediaPayload(payload string) (frameMediaClaims, bool) {
	parts := strings.Split(payload, ".")
	if len(parts) != 3 {
		return frameMediaClaims{}, false
	}
	userID, ok := parseCanonicalUint(parts[0])
	if !ok {
		return frameMediaClaims{}, false
	}
	deckID, ok := parseCanonicalUint(parts[1])
	if !ok {
		return frameMediaClaims{}, false
	}
	expiryUnix, ok := parseCanonicalInt(parts[2])
	if !ok {
		return frameMediaClaims{}, false
	}
	if userID == 0 || deckID == 0 || expiryUnix <= 0 {
		return frameMediaClaims{}, false
	}
	return frameMediaClaims{userID: userID, deckID: deckID, expiry: time.Unix(expiryUnix, 0).UTC()}, true
}

// parseCanonicalUint accepts only the plain decimal form of a non-negative integer.
func parseCanonicalUint(part string) (uint64, bool) {
	v, err := strconv.ParseUint(part, 10, 64)
	if err != nil || strconv.FormatUint(v, 10) != part {
		return 0, false
	}
	return v, true
}

// parseCanonicalInt accepts only the plain decimal form of a signed integer, which also rejects a leading plus or zeroes.
func parseCanonicalInt(part string) (int64, bool) {
	v, err := strconv.ParseInt(part, 10, 64)
	if err != nil || strconv.FormatInt(v, 10) != part {
		return 0, false
	}
	return v, true
}

// frameMediaSigningKey returns the HMAC key, creating it on first use.
//
// The mutex serialises creation inside this process. After writing the freshly generated key the function re-reads the
// setting and adopts the stored value: when two processes create the key at the same time the settings row is last-write-
// wins, so every process must sign with the value that actually landed in the database instead of the one it generated.
// The key is only ever held in memory and in the encrypted settings row; it is never logged.
func (s *Server) frameMediaSigningKey(ctx context.Context) ([]byte, error) {
	if s.secrets == nil || s.db == nil {
		return nil, errFrameMediaUnavailable
	}
	s.frameMediaKeyMu.Lock()
	defer s.frameMediaKeyMu.Unlock()
	if len(s.frameMediaKey) == frameMediaKeyBytes {
		return s.frameMediaKey, nil
	}
	stored, configured, err := store.GetSecret(ctx, s.db, s.secrets, frameMediaKeySetting)
	if err != nil {
		return nil, err
	}
	if configured {
		key, err := decodeFrameMediaKey(stored)
		if err != nil {
			return nil, err
		}
		s.frameMediaKey = key
		return key, nil
	}
	generated := make([]byte, frameMediaKeyBytes)
	if _, err := rand.Read(generated); err != nil {
		return nil, fmt.Errorf("generate frame media key: %w", err)
	}
	if err := store.PutSecret(ctx, s.db, s.secrets, frameMediaKeySetting,
		base64.RawURLEncoding.EncodeToString(generated), nil, time.Now().UTC()); err != nil {
		return nil, err
	}
	stored, configured, err = store.GetSecret(ctx, s.db, s.secrets, frameMediaKeySetting)
	if err != nil {
		return nil, err
	}
	if !configured {
		return nil, fmt.Errorf("frame media key %q disappeared after it was written", frameMediaKeySetting)
	}
	key, err := decodeFrameMediaKey(stored)
	if err != nil {
		return nil, err
	}
	s.frameMediaKey = key
	return key, nil
}

// decodeFrameMediaKey decodes the stored base64url key and enforces its length.
func decodeFrameMediaKey(stored string) ([]byte, error) {
	key, err := base64.RawURLEncoding.DecodeString(strings.TrimSpace(stored))
	if err != nil || len(key) != frameMediaKeyBytes {
		return nil, fmt.Errorf("stored %s is not a %d-byte base64url key", frameMediaKeySetting, frameMediaKeyBytes)
	}
	return key, nil
}

// absoluteMediaRefs turns deck-scoped media names into the absolute /media/<sha256> URLs the parent SPA needs.
//
// A field stores media by file name (<sha256>.<ext>, the form the data migration writes and the note write path
// normalises editor uploads into), because the frame resolves a name against its <base href>. The SPA has no base
// element, so the same HTML would resolve against the page address and 404. Only an img/video/audio src attribute, an
// anchor href attribute and a CSS url(...) reference are rewritten; the name must be exactly 64 lowercase hex characters
// with an optional extension, so ordinary text that happens to contain hex is left alone.
func absoluteMediaRefs(html string) string {
	return mediaNameRefRE.ReplaceAllString(html, "${1}/media/${2}")
}

// mediaNameRefRE matches one media name in a URL position: an src or href attribute or a CSS url() prefix, the 64-hex
// sha, and the optional extension segment that the /media/ route does not take.
var mediaNameRefRE = regexp.MustCompile(`((?:src|href)\s*=\s*["']|url\(\s*["']?)([0-9a-f]{64})(?:\.[^"')\s]*)?`)

// frameOutputHeader tells a card-face endpoint that the HTML is destined for the frame. Its absence is not an error:
// without the header the response keeps the absolute /media/ URLs the parent SPA needs, because the SPA has no <base>
// and would resolve a relative name against its own page. Only the value "1" marks a frame request.
const frameOutputHeader = "X-Engram-Frame"

// frameOutputRequested reports whether the caller asked for frame-ready HTML.
func frameOutputRequested(c *gin.Context) bool {
	return strings.TrimSpace(c.GetHeader(frameOutputHeader)) == "1"
}
