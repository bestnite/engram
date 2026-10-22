//! Parsing of the standard review-log (JSONL) and conversion to fsrs training items.
//!
//! The input schema matches the upstream fsrs-optimizer README:
//! `card_id / review_time(ms,UTC) / review_rating(1-4) / review_state(0-3) /
//! review_duration / timezone / day_start`.
//!
//! This module only does "read in -> validate -> group by card -> compute delta_t -> expand into cumulative `FSRSItem`s"
//! and never touches training itself. The conversion rules deliberately match upstream `examples/optimize.rs`:
//! the i-th review of each card yields one item, whose `reviews` are the card's first i+1 reviews,
//! and finally only items containing a long-term review (`long_term_review_cnt() > 0`) are kept.

use std::collections::BTreeMap;

use chrono::{Datelike, Duration, TimeZone, Utc};
use chrono_tz::Tz;
use fsrs::{FSRSItem, FSRSReview};
use serde::Deserialize;

/// The raw shape of a single review-log line.
///
/// `deny_unknown_fields` is deliberately omitted: the schema lets external tools attach extra fields, and unknown fields are
/// ignored, so a variety of export sources can be consumed without failing over one extra column.
#[derive(Debug, Deserialize)]
struct RawReview {
    card_id: serde_json::Value,
    review_time: i64,
    review_rating: u8,
    #[serde(default)]
    review_state: Option<u8>,
    #[serde(default)]
    review_duration: Option<i64>,
    #[serde(default)]
    timezone: Option<String>,
    #[serde(default)]
    day_start: Option<i64>,
}

/// After parsing, only the fields training actually needs are kept.
#[derive(Debug, Clone, Copy)]
struct ParsedReview {
    /// UTC milliseconds, used only for ordering within the same card.
    review_time: i64,
    /// 1..=4。
    rating: u32,
    /// Local "review day" ordinal (already normalized by timezone + day_start), used to compute delta_t.
    day_index: i64,
}

/// A dataset that can be fed straight to the trainer.
pub struct Dataset {
    /// Training items, ordered by the canonical card key (independent of input line order).
    pub items: Vec<FSRSItem>,
    /// Card ids aligned with `items`; the same card shares one i64.
    pub card_ids: Vec<i64>,
    /// The number of review records in the input.
    pub review_count: usize,
    /// The number of cards in the input.
    pub card_count: usize,
}

/// Parses the whole review-log text and converts it into a training set; errors return readable English messages.
///
/// Cards are collected in a `BTreeMap`, so item order depends only on the canonical card key and is **independent of input line
/// order**: reordering input lines changes neither the training-set order nor the weights.
pub fn parse(text: &str) -> Result<Dataset, String> {
    let mut by_card: BTreeMap<String, Vec<ParsedReview>> = BTreeMap::new();
    let mut review_count = 0usize;

    for (index, line) in text.lines().enumerate() {
        let line_no = index + 1;
        let trimmed = line.trim();
        if trimmed.is_empty() {
            continue;
        }
        let raw: RawReview = serde_json::from_str(trimmed)
            .map_err(|e| format!("line {line_no}: invalid JSON: {e}"))?;
        let key = canonical_card_id(&raw.card_id)
            .ok_or_else(|| format!("line {line_no}: card_id must be an integer or a string"))?;
        let review = convert(&raw, line_no)?;
        by_card.entry(key).or_default().push(review);
        review_count += 1;
    }

    if review_count == 0 {
        return Err("review log is empty: no review records found".to_string());
    }

    let card_count = by_card.len();
    let mut raw_items: Vec<FSRSItem> = Vec::new();
    let mut raw_ids: Vec<i64> = Vec::new();

    for (index, (_key, mut reviews)) in by_card.into_iter().enumerate() {
        // Ascending time within the same card; equal times are broken deterministically by rating,
        // so even with shuffled input lines the training-set order is unchanged (no reliance on an unstable sort order).
        reviews.sort_by_key(|r| (r.review_time, r.rating));
        let card_id = index as i64;
        let mut history: Vec<FSRSReview> = Vec::with_capacity(reviews.len());
        for (i, r) in reviews.iter().enumerate() {
            let delta_t = if i == 0 {
                // The first review's delta_t must be 0 (a hard fsrs requirement).
                0
            } else {
                // Repeated reviews on the same day have delta_t = 0; negative values (time going backwards) are clamped to 0 to avoid u32 underflow.
                (r.day_index - reviews[i - 1].day_index).max(0) as u32
            };
            history.push(FSRSReview {
                rating: r.rating,
                delta_t,
            });
            raw_items.push(FSRSItem {
                reviews: history.clone(),
            });
            raw_ids.push(card_id);
        }
    }

    // Matching examples/optimize.rs: drop items with no long-term review (history that is only the first delta_t=0).
    let mut items = Vec::with_capacity(raw_items.len());
    let mut card_ids = Vec::with_capacity(raw_ids.len());
    for (item, id) in raw_items.into_iter().zip(raw_ids) {
        if item.long_term_review_cnt() > 0 {
            items.push(item);
            card_ids.push(id);
        }
    }

    Ok(Dataset {
        items,
        card_ids,
        review_count,
        card_count,
    })
}

/// Validates and normalizes a single record.
fn convert(raw: &RawReview, line_no: usize) -> Result<ParsedReview, String> {
    if !(1..=4).contains(&raw.review_rating) {
        return Err(format!(
            "line {line_no}: review_rating must be 1..=4, got {}",
            raw.review_rating
        ));
    }
    if let Some(state) = raw.review_state {
        if state > 3 {
            return Err(format!(
                "line {line_no}: review_state must be 0..=3, got {state}"
            ));
        }
    }
    if let Some(duration) = raw.review_duration {
        if duration < 0 {
            return Err(format!(
                "line {line_no}: review_duration must be non-negative, got {duration}"
            ));
        }
    }
    let day_start = raw.day_start.unwrap_or(0);
    if !(0..=23).contains(&day_start) {
        return Err(format!(
            "line {line_no}: day_start must be 0..=23, got {day_start}"
        ));
    }
    let tz: Tz = raw
        .timezone
        .as_deref()
        .unwrap_or("UTC")
        .parse()
        .map_err(|_| format!("line {line_no}: unknown IANA time zone"))?;
    let utc = Utc
        .timestamp_millis_opt(raw.review_time)
        .single()
        .ok_or_else(|| format!("line {line_no}: review_time is out of range"))?;
    // Review day definition: shift local time back by day_start hours and take the date; this matches the scheduler's day-rollover rule.
    let local = utc.with_timezone(&tz) - Duration::hours(day_start);
    let day_index = local.date_naive().num_days_from_ce() as i64;

    Ok(ParsedReview {
        review_time: raw.review_time,
        rating: raw.review_rating as u32,
        day_index,
    })
}

/// Normalizes card_id into a sortable, deduplicable canonical key.
///
/// Numeric and string namespaces are separated by a prefix, and numbers are zero-padded to a fixed width so that `BTreeMap`
/// sorts keys in natural (numeric) order, making item order independent of input line order and unaffected by `10` vs `2` lexicographic order.
fn canonical_card_id(value: &serde_json::Value) -> Option<String> {
    match value {
        serde_json::Value::String(s) => Some(format!("s:{s}")),
        serde_json::Value::Number(n) => n.as_i64().map(|i| format!("n:{i:020}")),
        _ => None,
    }
}
