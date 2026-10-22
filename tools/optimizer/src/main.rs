//! FSRS parameter optimizer adapter (ROADMAP.md M9-2).
//!
//! Single responsibility: read a standard review-log (JSONL) -> call the `fsrs` crate optimizer -> write 21-element weight JSON.
//! All algorithm implementation comes from upstream `fsrs`; this project rewrites no FSRS logic (hard constraint:
//! the algorithm never goes into Go code). This binary ships with the main program and is invoked as a subprocess by `internal/jobs`.
//!
//! The CLI contract (arguments, input/output format, exit codes, determinism) is documented in `tools/optimizer/README.md`.

mod review_log;

use std::fs;
use std::io::{self, Read, Write};
use std::process::ExitCode;

const VERSION: &str = env!("CARGO_PKG_VERSION");
/// The FSRS v6 weight dimension; fewer or more means the upstream API changed, and it must fail explicitly rather than output silently.
const WEIGHT_COUNT: usize = 21;
/// Default training seed, matching `fsrs::TrainingConfig::default()`, so that "the default is deterministic".
const DEFAULT_SEED: u64 = 2023;
/// Single-threaded by default: burn/rayon parallel reduction can make results depend on thread scheduling, and one thread is the cheapest determinism guarantee.
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

/// Command-line configuration.
#[derive(Debug)]
struct Config {
    /// Input path; `None` or `Some("-")` means stdin.
    input: Option<String>,
    /// Output path; `None` or `Some("-")` means stdout.
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

/// Structured error: carries only a stable exit code and a one-line English description.
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
    // fsrs trains with rayon internally; the thread count must be written to the environment variable before the pool is first built, otherwise the default does not take effect.
    std::env::set_var("RAYON_NUM_THREADS", config.threads.to_string());

    let text = read_input(config.input.as_deref())?;
    let dataset = review_log::parse(&text).map_err(AppError::io)?;

    if dataset.items.is_empty() {
        return Err(AppError::io(
            "no review history left after filtering; nothing to train on",
        ));
    }
    // fsrs just returns the default weights when the training set is smaller than 8; that amounts to "no optimization", and erroring out is more honest than returning defaults.
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

/// Calls the upstream optimizer and post-validates its output.
fn train(dataset: &review_log::Dataset, seed: u64) -> Result<Vec<f32>, String> {
    let input = fsrs::ComputeParametersInput {
        train_set: dataset.items.clone(),
        // Pass card_ids so training can group by card and reuse prefixes (that is exactly what the upstream field is for).
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

    // Dimension and finiteness must be enforced: the weights get persisted and consumed by the scheduler, so a wrong dimension/NaN must fail right here.
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
