//! 标准 review-log（JSONL）的解析与到 fsrs 训练项的转换。
//!
//! 输入 schema 与上游 fsrs-optimizer 的 README 一致：
//! `card_id / review_time(ms,UTC) / review_rating(1-4) / review_state(0-3) /
//! review_duration / timezone / day_start`。
//!
//! 本模块只负责「读入 -> 校验 -> 按卡分组 -> 计算 delta_t -> 展开为累计 `FSRSItem`」，
//! 不接触训练本身。转换规则刻意与上游 `examples/optimize.rs` 保持一致：
//! 每张卡的第 i 次复习生成一个 item，其 `reviews` 为该卡前 i+1 次复习，
//! 最后只保留含长期复习（`long_term_review_cnt() > 0`）的 item。

use std::collections::BTreeMap;

use chrono::{Datelike, Duration, TimeZone, Utc};
use chrono_tz::Tz;
use fsrs::{FSRSItem, FSRSReview};
use serde::Deserialize;

/// 单行 review-log 的原始形态。
///
/// 刻意不加 `deny_unknown_fields`：schema 允许外部工具附加字段，未知字段一律忽略，
/// 这样才能接各种导出源而不因多一个列就失败。
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

/// 解析后只保留训练真正需要的字段。
#[derive(Debug, Clone, Copy)]
struct ParsedReview {
    /// UTC 毫秒，仅用于同卡内排序。
    review_time: i64,
    /// 1..=4。
    rating: u32,
    /// 本地「复习日」序号（已按 timezone + day_start 归一），用于算 delta_t。
    day_index: i64,
}

/// 一份可直接喂给训练器的数据集。
pub struct Dataset {
    /// 训练项，顺序由卡片规范键决定（与输入行顺序无关）。
    pub items: Vec<FSRSItem>,
    /// 与 `items` 对齐的卡片 id；同一张卡共享同一个 i64。
    pub card_ids: Vec<i64>,
    /// 输入里的复习记录条数。
    pub review_count: usize,
    /// 输入里的卡片数。
    pub card_count: usize,
}

/// 解析整份 review-log 文本并转换为训练集；出错返回英文可读信息。
///
/// 卡片用 `BTreeMap` 收集，因此 item 顺序只取决于卡片规范键，**与输入行顺序无关**：
/// 重排输入行不会改变训练集顺序，也就不会改变权重。
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
        // 同一张卡内按时间升序；时间相同时再用评分做确定性次序，
        // 这样即使输入行被打乱，训练集顺序也不变（避免依赖不稳定的排序次序）。
        reviews.sort_by_key(|r| (r.review_time, r.rating));
        let card_id = index as i64;
        let mut history: Vec<FSRSReview> = Vec::with_capacity(reviews.len());
        for (i, r) in reviews.iter().enumerate() {
            let delta_t = if i == 0 {
                // 首次复习的 delta_t 必须为 0（fsrs 的硬要求）。
                0
            } else {
                // 同一天内的重复复习 delta_t = 0；负值（时间倒挂）夹到 0，避免 u32 下溢。
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

    // 与 examples/optimize.rs 一致：丢掉没有长期复习的项（只有首次 delta_t=0 的历史）。
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

/// 校验并归一单条记录。
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
    // 复习日的定义：把本地时间往前推 day_start 小时后取日期；与调度侧的跨天规则一致。
    let local = utc.with_timezone(&tz) - Duration::hours(day_start);
    let day_index = local.date_naive().num_days_from_ce() as i64;

    Ok(ParsedReview {
        review_time: raw.review_time,
        rating: raw.review_rating as u32,
        day_index,
    })
}

/// 把 card_id 归一成可排序、可去重的规范键。
///
/// 数字与字符串命名空间用前缀分开，且数字零填充到定宽，使 `BTreeMap` 按键的自然序即数值序，
/// 让 item 顺序既不依赖输入行顺序，也不受 `10` vs `2` 字典序的影响。
fn canonical_card_id(value: &serde_json::Value) -> Option<String> {
    match value {
        serde_json::Value::String(s) => Some(format!("s:{s}")),
        serde_json::Value::Number(n) => n.as_i64().map(|i| format!("n:{i:020}")),
        _ => None,
    }
}
