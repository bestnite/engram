package api

import (
	"encoding/binary"
	"hash/fnv"
	"math/rand"
)

// OptionOrder returns the display order of a choice card's options: display slot b shows
// stored option index order[b]. n is the number of stored options; n < 2 returns nil.
// The order is derived from (userID, cardID, version) and never persisted, so it is stable
// across refreshes and transports for the same unreviewed card and changes after a review.
//
// Display order is presentation only: the stored options and the submitted answer keep
// their stored indices, so a client maps a clicked display slot through this slice.
func OptionOrder(n int, userID, cardID uint64, version int) []int {
	if n < 2 {
		return nil
	}
	h := fnv.New64a()
	var buf [8]byte
	for _, v := range []uint64{userID, cardID, uint64(version)} {
		binary.BigEndian.PutUint64(buf[:], v)
		_, _ = h.Write(buf[:])
	}
	r := rand.New(rand.NewSource(int64(h.Sum64())))
	order := make([]int, n)
	for i := range order {
		order[i] = i
	}
	r.Shuffle(n, func(i, j int) { order[i], order[j] = order[j], order[i] })
	return order
}

// optionCount returns the number of stored options held by the fields value named field;
// it returns 0 when the value is absent or is not an array.
func optionCount(fields map[string]any, field string) int {
	switch v := fields[field].(type) {
	case []any:
		return len(v)
	case []string:
		return len(v)
	}
	return 0
}
