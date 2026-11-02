package apkg

import (
	"encoding/json"
	"fmt"
	"sort"

	"gorm.io/gorm"
)

func readScheduling(db *gorm.DB, pkg *Package) error {
	if err := db.Raw("SELECT " + roundedIntegers("crt") + " FROM col").Row().Scan(&pkg.CreatedAt); err != nil {
		return fmt.Errorf("%w: collection creation time: %v", ErrCorruptCollection, err)
	}
	pkg.RevlogErr = readReviews(db, pkg)
	if pkg.Format == FormatModern {
		return readModernDeckConfigs(db, pkg)
	}
	return readLegacyDeckConfigs(db, pkg)
}

func readReviews(db *gorm.DB, pkg *Package) error {
	rows, err := db.Raw("SELECT id, " + rejectRealIntegers("cid") + ", " + roundedIntegers("usn") + ", " + rejectRealIntegers("ease") + ", " + roundedIntegers("ivl", "lastIvl", "factor", "time") + ", " + rejectRealIntegers("type") + ", typeof(cid), typeof(ease), typeof(type) FROM revlog ORDER BY id").Rows()
	if err != nil {
		return fmt.Errorf("%w: revlog: %v", ErrCorruptCollection, err)
	}
	defer rows.Close()
	var scanErr error
	for rows.Next() {
		var r Review
		var cidType, easeType, typeType string
		if err := rows.Scan(&r.ID, &r.CardID, &r.USN, &r.Ease, &r.Interval, &r.LastInterval, &r.Factor, &r.Time, &r.Type, &cidType, &easeType, &typeType); err != nil {
			pkg.ReviewsUnreadable++
			if scanErr == nil {
				scanErr = fmt.Errorf("%w: revlog: %v", ErrCorruptCollection, err)
			}
			continue
		}
		var realErr error
		for i, kind := range []string{cidType, easeType, typeType} {
			if kind == "real" {
				realErr = fmt.Errorf("%w: revlog.%s holds a REAL value", ErrCorruptCollection, []string{"cid", "ease", "type"}[i])
				break
			}
		}
		if realErr != nil {
			pkg.ReviewsUnreadable++
			if scanErr == nil {
				scanErr = realErr
			}
			continue
		}
		pkg.Reviews = append(pkg.Reviews, r)
	}
	if err := rows.Err(); err != nil {
		return fmt.Errorf("%w: revlog: %v", ErrCorruptCollection, err)
	}
	return scanErr
}

func readLegacyDeckConfigs(db *gorm.DB, pkg *Package) error {
	var raw string
	if err := db.Raw("SELECT dconf FROM col").Row().Scan(&raw); err != nil {
		return fmt.Errorf("%w: col.dconf: %v", ErrCorruptCollection, err)
	}
	var configs map[string]json.RawMessage
	if err := json.Unmarshal([]byte(raw), &configs); err != nil {
		pkg.DeckConfigsSkipped = true
		return nil
	}
	for _, rawConfig := range configs {
		var c struct {
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
		if err := json.Unmarshal(rawConfig, &c); err != nil {
			// The deck gets preset defaults when its config is skipped, and content still imports.
			pkg.DeckConfigsSkipped = true
			continue
		}
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
			// The deck gets preset defaults when its config is skipped, and content still imports.
			pkg.DeckConfigsSkipped = true
			continue
		}
		cfg.ID, cfg.Name = id, name
		pkg.DeckConfigs = append(pkg.DeckConfigs, cfg)
	}
	if err := rows.Err(); err != nil {
		return fmt.Errorf("%w: deck_config: %v", ErrCorruptCollection, err)
	}
	return nil
}
