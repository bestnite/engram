//! FSRS 参数优化器适配器（ROADMAP.md M9-2）。
//!
//! 职责单一：读标准 review-log（JSONL）-> 调 `fsrs` crate 的优化器 -> 写出 21 元权重 JSON。
//! 算法实现全部来自上游 `fsrs`，本项目不重写任何 FSRS 逻辑（硬约束：
//! 算法永不进 Go 代码）。本二进制与主程序一起分发，由 `internal/jobs` 以子进程方式调用。
//!
//! CLI 契约（参数、输入输出格式、退出码、确定性）写在 `tools/optimizer/README.md`。

mod review_log;

use std::fs;
use std::io::{self, Read, Write};
use std::process::ExitCode;

const VERSION: &str = env!("CARGO_PKG_VERSION");
/// FSRS v6 的权重维度；少了或多了都说明上游 API 变了，必须显式失败而不是静默输出。
const WEIGHT_COUNT: usize = 21;
/// 训练种子默认值，与 `fsrs::TrainingConfig::default()` 一致，保证「默认即确定」。
const DEFAULT_SEED: u64 = 2023;
/// 默认单线程：burn/rayon 的并行归约可能让结果依赖线程调度，单线程是最低成本的确定性保证。
const DEFAULT_THREADS: usize = 1;

const USAGE: &str = "\
optimizer - FSRS parameter optimiser adapter

USAGE:
    optimizer [OPTIONS] [INPUT]

ARGUMENTS:
    [INPUT]  Path to a review-log JSONL file. Omit or use \"-\" to read stdin.

OPTIONS:
    --out <PATH>     Write the weights JSON to PATH. \"-\" or omitted means stdout.
    --threads <N>    Worker threads for training (default 1). Values > 1 may make
                     the weights depend on thread scheduling.
    --seed <N>       Training seed (default 2023, the upstream default).
    -h, --help       Print this help.
    -V, --version    Print the adapter version.

INPUT (one JSON object per line):
    card_id         integer or string
    review_time     milliseconds since the Unix epoch, UTC
    review_rating   1..4 (Again/Hard/Good/Easy)
    review_state    0..3 (New/Learning/Review/Relearning), optional
    review_duration milliseconds, optional
    timezone        IANA time zone, optional (default UTC)
    day_start       hour 0..23 that starts a new day, optional (default 0)

OUTPUT:
    A JSON array of exactly 21 numbers on stdout (or in --out). Diagnostics go to
    stderr in English.

EXIT CODES:
    0  success
    2  usage error
    3  input/output error (cannot read, parse, validate or write)
    4  training error (the optimiser refused the data or failed)

DETERMINISM:
    With the default single worker thread and the fixed seed, the output is
    byte-for-byte reproducible for the same input.
";

fn main() -> ExitCode {
    let args: Vec<String> = std::env::args().skip(1).collect();
    match run(&args) {
        Ok(()) => ExitCode::SUCCESS,
        Err(err) => {
            eprintln!("optimizer: error: {}", err.message);
            ExitCode::from(err.code)
        }
    }
}

fn run(args: &[String]) -> Result<(), AppError> {
    match parse_args(args)? {
        Action::Help => {
            print!("{USAGE}");
            Ok(())
        }
        Action::Version => {
            println!("optimizer {VERSION}");
            Ok(())
        }
        Action::Run(config) => execute(config),
    }
}

/// 命令行配置。
#[derive(Debug)]
struct Config {
    /// 输入路径；`None` 或 `Some("-")` 表示 stdin。
    input: Option<String>,
    /// 输出路径；`None` 或 `Some("-")` 表示 stdout。
    out: Option<String>,
    threads: usize,
    seed: u64,
}

#[derive(Debug)]
enum Action {
    Run(Config),
    Help,
    Version,
}

/// 结构化错误：只携带一个稳定的退出码和一行英文说明。
#[derive(Debug)]
struct AppError {
    code: u8,
    message: String,
}

impl AppError {
    fn usage(message: impl Into<String>) -> Self {
        Self {
            code: 2,
            message: message.into(),
        }
    }

    fn io(message: impl Into<String>) -> Self {
        Self {
            code: 3,
            message: message.into(),
        }
    }

    fn training(message: impl Into<String>) -> Self {
        Self {
            code: 4,
            message: message.into(),
        }
    }
}

fn parse_args(args: &[String]) -> Result<Action, AppError> {
    let mut input: Option<String> = None;
    let mut out: Option<String> = None;
    let mut threads = DEFAULT_THREADS;
    let mut seed = DEFAULT_SEED;

    let mut i = 0;
    while i < args.len() {
        match args[i].as_str() {
            "-h" | "--help" => return Ok(Action::Help),
            "-V" | "--version" => return Ok(Action::Version),
            "--out" => {
                i += 1;
                let value = args
                    .get(i)
                    .ok_or_else(|| AppError::usage("--out requires a path"))?;
                out = Some(value.clone());
            }
            "--threads" => {
                i += 1;
                threads = parse_positive(args.get(i), "--threads")?;
            }
            "--seed" => {
                i += 1;
                seed = parse_u64(args.get(i), "--seed")?;
            }
            other if other.starts_with('-') && other != "-" => {
                return Err(AppError::usage(format!("unknown option: {other}")));
            }
            other => {
                if input.is_some() {
                    return Err(AppError::usage("only one input path is allowed"));
                }
                input = Some(other.to_string());
            }
        }
        i += 1;
    }

    if threads == 0 {
        return Err(AppError::usage("--threads must be >= 1"));
    }
    Ok(Action::Run(Config {
        input,
        out,
        threads,
        seed,
    }))
}

fn parse_positive(value: Option<&String>, flag: &str) -> Result<usize, AppError> {
    let value = value.ok_or_else(|| AppError::usage(format!("{flag} requires a value")))?;
    value.parse::<usize>().map_err(|_| {
        AppError::usage(format!(
            "{flag} expects a non-negative integer, got {value}"
        ))
    })
}

fn parse_u64(value: Option<&String>, flag: &str) -> Result<u64, AppError> {
    let value = value.ok_or_else(|| AppError::usage(format!("{flag} requires a value")))?;
    value.parse::<u64>().map_err(|_| {
        AppError::usage(format!(
            "{flag} expects a non-negative integer, got {value}"
        ))
    })
}

fn execute(config: Config) -> Result<(), AppError> {
    // fsrs 内部用 rayon 训练；线程数必须在初次建池前写进环境变量，否则默认值生效不了。
    std::env::set_var("RAYON_NUM_THREADS", config.threads.to_string());

    let text = read_input(config.input.as_deref())?;
    let dataset = review_log::parse(&text).map_err(AppError::io)?;

    if dataset.items.is_empty() {
        return Err(AppError::io(
            "no review history left after filtering; nothing to train on",
        ));
    }
    // fsrs 在训练集小于 8 时直接返回默认权重；那等于「没优化」，报错比返回默认值更诚实。
    if dataset.items.len() < 8 {
        return Err(AppError::io(format!(
            "not enough review history to optimise: {} usable items (minimum 8)",
            dataset.items.len()
        )));
    }

    eprintln!(
        "optimizer: read {} reviews for {} cards, {} training items",
        dataset.review_count,
        dataset.card_count,
        dataset.items.len()
    );
    eprintln!(
        "optimizer: training with seed {} on {} thread(s)",
        config.seed, config.threads
    );

    let weights = train(&dataset, config.seed).map_err(AppError::training)?;

    let json = serde_json::to_string(&weights)
        .map_err(|e| AppError::training(format!("serialise weights: {e}")))?;
    write_output(config.out.as_deref(), &json)?;

    eprintln!("optimizer: wrote {} weights", weights.len());
    Ok(())
}

/// 调上游优化器并做输出后置校验。
fn train(dataset: &review_log::Dataset, seed: u64) -> Result<Vec<f32>, String> {
    let input = fsrs::ComputeParametersInput {
        train_set: dataset.items.clone(),
        // 带上 card_ids，让训练按卡分组做前缀复用（上游字段的用途即此）。
        card_ids: Some(dataset.card_ids.clone()),
        progress: None,
        enable_short_term: true,
        num_relearning_steps: None,
        training_config: Some(fsrs::TrainingConfig {
            seed,
            ..fsrs::TrainingConfig::default()
        }),
    };

    let weights = fsrs::compute_parameters(input).map_err(|e| format!("training failed: {e}"))?;

    // 维度与有限性必须守住：权重会被落库并被排程器消费，错维度/NaN 必须在这里就失败。
    if weights.len() != WEIGHT_COUNT {
        return Err(format!(
            "optimiser returned {} weights, expected {WEIGHT_COUNT}",
            weights.len()
        ));
    }
    if weights.iter().any(|w| !w.is_finite()) {
        return Err("optimiser returned a non-finite weight".to_string());
    }
    Ok(weights)
}

fn read_input(path: Option<&str>) -> Result<String, AppError> {
    match path {
        None | Some("-") => {
            let mut buffer = String::new();
            io::stdin()
                .read_to_string(&mut buffer)
                .map_err(|e| AppError::io(format!("read stdin: {e}")))?;
            Ok(buffer)
        }
        Some(path) => {
            fs::read_to_string(path).map_err(|e| AppError::io(format!("read {path}: {e}")))
        }
    }
}

fn write_output(out: Option<&str>, json: &str) -> Result<(), AppError> {
    match out {
        None | Some("-") => {
            let mut stdout = io::stdout().lock();
            writeln!(stdout, "{json}").map_err(|e| AppError::io(format!("write stdout: {e}")))
        }
        Some(path) => {
            let mut file =
                fs::File::create(path).map_err(|e| AppError::io(format!("create {path}: {e}")))?;
            writeln!(file, "{json}").map_err(|e| AppError::io(format!("write {path}: {e}")))
        }
    }
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn parses_flags_and_positional_input() {
        let args: Vec<String> = ["--out", "w.json", "--threads", "2", "log.jsonl"]
            .iter()
            .map(|s| s.to_string())
            .collect();
        match parse_args(&args).unwrap() {
            Action::Run(config) => {
                assert_eq!(config.input.as_deref(), Some("log.jsonl"));
                assert_eq!(config.out.as_deref(), Some("w.json"));
                assert_eq!(config.threads, 2);
                assert_eq!(config.seed, DEFAULT_SEED);
            }
            _ => panic!("expected run action"),
        }
    }

    #[test]
    fn rejects_unknown_option() {
        let args = vec!["--nope".to_string()];
        let err = parse_args(&args).unwrap_err();
        assert_eq!(err.code, 2);
    }

    #[test]
    fn help_and_version_short_circuit() {
        assert!(matches!(
            parse_args(&["--help".to_string()]).unwrap(),
            Action::Help
        ));
        assert!(matches!(
            parse_args(&["-V".to_string()]).unwrap(),
            Action::Version
        ));
    }
}
