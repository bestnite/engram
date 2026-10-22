package web

import (
	"io/fs"
	"os"
	"path/filepath"
	"time"
)

// Sampling of the media directory footprint (health page).
//
// Trade-off: a full recursive scan of the media directory inside one request would stall the request when there are many files (media files are scattered across
// two directory levels keyed by sha256, and the scale only ever grows). So there are two layers of protection here:
//   - Cap: count at most mediaSizeMaxEntries directory entries, then stop and mark truncated.
//     The value returned then means "at least this many bytes" — better a lower bound than a request hanging indefinitely.
//   - Cache: reuse the previous result within mediaSizeTTL, so refreshing the health page does not rescan every time.
//
// Cost: the number reads low when truncated, and may lag within the cache window. The health page labels both cases explicitly,
// rather than approximating silently.

const (
	// mediaSizeTTL is how long a media-usage reading is cached.
	mediaSizeTTL = 30 * time.Second
	// mediaSizeMaxEntries is the maximum number of directory entries a single count may walk.
	mediaSizeMaxEntries = 20000
)

// mediaSizeSample is the result of one media-usage sample.
type mediaSizeSample struct {
	bytes     int64
	truncated bool
	at        time.Time
}

// cappedDirSize recursively totals a directory's bytes, walking at most maxEntries directory entries;
// on overflow it returns the bytes accumulated so far with truncated=true. A missing directory counts as 0.
func cappedDirSize(root string, maxEntries int) (int64, bool, error) {
	var total int64
	entries := 0
	truncated := false
	err := filepath.WalkDir(root, func(_ string, d fs.DirEntry, err error) error {
		if err != nil {
			if os.IsNotExist(err) {
				return nil
			}
			return err
		}
		if entries >= maxEntries {
			truncated = true
			return fs.SkipAll
		}
		entries++
		if d.Type().IsRegular() {
			info, ierr := d.Info()
			if ierr != nil {
				return ierr
			}
			total += info.Size()
		}
		return nil
	})
	if err != nil && !os.IsNotExist(err) {
		return 0, false, err
	}
	return total, truncated, nil
}

// mediaUsage returns the media directory footprint (bytes) and whether it was truncated by the cap.
// Returns 0 when media storage is not wired up; a sample within the TTL reuses the cache directly.
func (s *Server) mediaUsage() (int64, bool) {
	if s.media == nil {
		return 0, false
	}
	now := time.Now()
	s.mediaSizeMu.Lock()
	if s.mediaSize != nil && now.Sub(s.mediaSize.at) < mediaSizeTTL {
		sample := *s.mediaSize
		s.mediaSizeMu.Unlock()
		return sample.bytes, sample.truncated
	}
	s.mediaSizeMu.Unlock()

	bytes, truncated, err := cappedDirSize(s.media.Root(), mediaSizeMaxEntries)
	if err != nil {
		// A failed directory read does not block the health page: log in English and return 0 (the media directory may not exist yet).
		s.logger.Error("health: compute media directory size failed", "error", err)
		return 0, false
	}

	s.mediaSizeMu.Lock()
	s.mediaSize = &mediaSizeSample{bytes: bytes, truncated: truncated, at: now}
	s.mediaSizeMu.Unlock()
	return bytes, truncated
}
