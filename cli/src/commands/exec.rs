use std::collections::HashSet;
use std::process::ExitCode;

use anyhow::{Context, Result, anyhow};
use tokio::process::Command as TokioCommand;
use zeroize::Zeroizing;

use crate::api::ApiClient;

pub async fn run(
    client: &ApiClient,
    names: Vec<String>,
    all: bool,
    only: Vec<String>,
    argv: Vec<String>,
) -> Result<()> {
    if argv.is_empty() {
        return Err(anyhow!("nothing to exec — pass command after `--`"));
    }
    if !names.is_empty() && all {
        return Err(anyhow!("--names conflicts with --all"));
    }

    let me = client.me().await?;
    let target: Option<Vec<String>> = if all || (names.is_empty() && only.is_empty()) {
        None
    } else if !names.is_empty() {
        Some(names)
    } else {
        None
    };

    let resp = client.reveal(&me.vault_slug, target.as_deref()).await?;
    if !resp.missing.is_empty() {
        eprintln!(
            "ferrvault: {} secret(s) requested but missing: {}",
            resp.missing.len(),
            resp.missing.join(", ")
        );
    }

    let only_set: Option<HashSet<&str>> = if only.is_empty() {
        None
    } else {
        Some(only.iter().map(String::as_str).collect())
    };

    let mut cmd = TokioCommand::new(&argv[0]);
    if argv.len() > 1 {
        cmd.args(&argv[1..]);
    }

    let mut injected = 0u32;
    for hit in &resp.hits {
        if let Some(ref set) = only_set
            && !set.contains(hit.name.as_str())
        {
            continue;
        }
        let value = Zeroizing::new(hit.value.clone());
        cmd.env(&hit.name, value.as_str());
        injected += 1;
    }

    if injected == 0 {
        eprintln!("ferrvault: warning — exec running with 0 secrets injected");
    } else {
        eprintln!("ferrvault: injecting {injected} secret(s) into child process env");
    }

    let mut child = cmd
        .spawn()
        .with_context(|| format!("spawning `{}`", argv[0]))?;
    let status = child.wait().await.context("waiting for child")?;

    let code = status.code().unwrap_or(1);
    if !status.success() {
        std::process::exit(code);
    }
    let _ = ExitCode::from(u8::try_from(code).unwrap_or(0));
    Ok(())
}
