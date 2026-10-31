package anki

import (
	"encoding/json"
	"fmt"
	"sort"

	"gorm.io/gorm"
)

func readScheduling(db *gorm.DB, pkg *Package) error {
	if err := db.Raw("SELECT crt FROM col").Row().Scan(&pkg.CreatedAt); err != nil {
		return fmt.Errorf("%w: collection creation time: %v", ErrCorruptCollection, err)
	}
	rows, err := db.Raw("SELECT id, cid, usn, ease, ivl, lastIvl, factor, time, type FROM revlog ORDER BY id").Rows()
	if err != nil {
		return fmt.Errorf("%w: revlog: %v", ErrCorruptCollection, err)
	}
	for rows.Next() {
		var r Review
		if err := rows.Scan(&r.ID, &r.CardID, &r.USN, &r.Ease, &r.Interval, &r.LastInterval, &r.Factor, &r.Time, &r.Type); err != nil {
			rows.Close()
			return fmt.Errorf("%w: revlog: %v", ErrCorruptCollection, err)
		}
		pkg.Reviews = append(pkg.Reviews, r)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return fmt.Errorf("%w: revlog: %v", ErrCorruptCollection, err)
	}
	if pkg.Format == FormatModern {
		return readModernDeckConfigs(db, pkg)
	}
	return readLegacyDeckConfigs(db, pkg)
}

func readLegacyDeckConfigs(db *gorm.DB, pkg *Package) error {
	var raw string
	if err := db.Raw("SELECT dconf FROM col").Row().Scan(&raw); err != nil {
		return fmt.Errorf("%w: col.dconf: %v", ErrCorruptCollection, err)
	}
	var configs map[string]struct {
		ID        int64     `json:"id"`
		Name      string    `json:"name"`
		Retention float64   `json:"desiredRetention"`
		FSRS6     []float64 `json:"fsrsParams6"`
		FSRS5     []float64 `json:"fsrsParams5"`
		FSRS4     []float64 `json:"fsrsWeights"`
		New       struct {
			Delays []float64 `json:"delays"`
		} `json:"new"`
		Lapse struct {
			Delays []float64 `json:"delays"`
		} `json:"lapse"`
		Review struct {
			Max int `json:"maxIvl"`
		} `json:"rev"`
	}
	if err := json.Unmarshal([]byte(raw), &configs); err != nil {
		return fmt.Errorf("%w: col.dconf: %v", ErrCorruptCollection, err)
	}
	for _, c := range configs {
		weights := c.FSRS6
		if len(weights) == 0 {
			weights = c.FSRS5
		}
		if len(weights) == 0 {
			weights = c.FSRS4
		}
		pkg.DeckConfigs = append(pkg.DeckConfigs, DeckConfig{ID: c.ID, Name: c.Name, DesiredRetention: c.Retention, LearnSteps: c.New.Delays, RelearnSteps: c.Lapse.Delays, MaximumReviewInterval: c.Review.Max, Weights: weights})
	}
	sort.Slice(pkg.DeckConfigs, func(i, j int) bool { return pkg.DeckConfigs[i].ID < pkg.DeckConfigs[j].ID })
	return nil
}

func readModernDeckConfigs(db *gorm.DB, pkg *Package) error {
	rows, err := db.Raw("SELECT id, name, config FROM deck_config ORDER BY id").Rows()
	if err != nil {
		return fmt.Errorf("%w: deck_config: %v", ErrCorruptCollection, err)
	}
	defer rows.Close()
	for rows.Next() {
		var id int64
		var name string
		var data []byte
		if err := rows.Scan(&id, &name, &data); err != nil {
			return fmt.Errorf("%w: deck_config: %v", ErrCorruptCollection, err)
		}
		cfg, err := decodeDeckConfig(data)
		if err != nil {
			return fmt.Errorf("%w: deck_config: %v", ErrCorruptCollection, err)
		}
		cfg.ID, cfg.Name = id, name
		pkg.DeckConfigs = append(pkg.DeckConfigs, cfg)
	}
	if err := rows.Err(); err != nil {
		return fmt.Errorf("%w: deck_config: %v", ErrCorruptCollection, err)
	}
	return nil
}
