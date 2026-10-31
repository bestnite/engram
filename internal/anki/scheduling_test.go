package anki

import (
	"encoding/binary"
	"math"
	"reflect"
	"testing"

	"github.com/glebarez/sqlite"
	"google.golang.org/protobuf/encoding/protowire"
	"gorm.io/gorm"
)

func TestSchedulingRows(t *testing.T) {
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	for _, query := range []string{
		`CREATE TABLE col (crt INTEGER, dconf TEXT)`,
		`INSERT INTO col VALUES (100, '{"1":{"id":1,"name":"x","desiredRetention":0.91,"fsrsParams6":[1,2],"fsrsParams5":[3],"fsrsWeights":[4],"new":{"delays":[1,10]},"lapse":{"delays":[5]},"rev":{"maxIvl":200}}}')`,
		`CREATE TABLE revlog (id INTEGER, cid INTEGER, usn INTEGER, ease INTEGER, ivl INTEGER, lastIvl INTEGER, factor INTEGER, time INTEGER, type INTEGER)`,
		`INSERT INTO revlog VALUES (123, 456, -1, 3, 7, 2, 2500, 800, 1)`,
	} {
		if err := db.Exec(query).Error; err != nil {
			t.Fatal(err)
		}
	}
	pkg := &Package{Format: FormatLegacy1}
	if err := readScheduling(db, pkg); err != nil {
		t.Fatal(err)
	}
	if len(pkg.Reviews) != 1 || pkg.Reviews[0] != (Review{123, 456, -1, 3, 7, 2, 2500, 800, 1}) {
		t.Fatalf("reviews: %+v", pkg.Reviews)
	}
	if len(pkg.DeckConfigs) != 1 || !reflect.DeepEqual(pkg.DeckConfigs[0].Weights, []float64{1, 2}) || pkg.DeckConfigs[0].MaximumReviewInterval != 200 {
		t.Fatalf("configs: %+v", pkg.DeckConfigs)
	}
}

func TestDecodeDeckConfig(t *testing.T) {
	var b []byte
	packed := func(num protowire.Number, values ...float32) {
		var raw []byte
		for _, v := range values {
			var bits [4]byte
			binary.LittleEndian.PutUint32(bits[:], math.Float32bits(v))
			raw = append(raw, bits[:]...)
		}
		b = protowire.AppendTag(b, num, protowire.BytesType)
		b = protowire.AppendBytes(b, raw)
	}
	packed(1, 1, 10)
	packed(2, 5)
	packed(3, 1)
	packed(5, 2)
	packed(6, 3, 4)
	b = protowire.AppendTag(b, 16, protowire.VarintType)
	b = protowire.AppendVarint(b, 1000)
	b = protowire.AppendTag(b, 37, protowire.Fixed32Type)
	b = protowire.AppendFixed32(b, math.Float32bits(.9))
	cfg, err := decodeDeckConfig(b)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(cfg.Weights, []float64{3, 4}) || !reflect.DeepEqual(cfg.LearnSteps, []float64{1, 10}) || !reflect.DeepEqual(cfg.RelearnSteps, []float64{5}) || cfg.MaximumReviewInterval != 1000 || cfg.DesiredRetention < .899 || cfg.DesiredRetention > .901 {
		t.Fatalf("config: %+v", cfg)
	}
	if _, err := decodeDeckConfig([]byte{0x32, 1, 0}); err == nil {
		t.Fatal("accepted malformed packed floats")
	}
}
