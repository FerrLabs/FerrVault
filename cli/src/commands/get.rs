use std::collections::BTreeMap;

use anyhow::{Result, anyhow};

use crate::api::{ApiClient, BulkReadHit};
use crate::cli::OutputFormat;

pub async fn run(
    client: &ApiClient,
    name: Option<String>,
    all: bool,
    names: Vec<String>,
    format: OutputFormat,
) -> Result<()> {
    let me = client.me().await?;

    let target: Option<Vec<String>> = match (name.clone(), all, names.is_empty()) {
        (Some(n), false, true) => Some(vec![n]),
        (None, true, true) => None,
        (None, false, false) => Some(names.clone()),
        (None, false, true) => {
            return Err(anyhow!(
                "pass a single secret NAME, --all, or --names A,B,C"
            ));
        }
        _ => return Err(anyhow!("--all conflicts with NAME and --names")),
    };

    let resp = client.reveal(&me.vault_slug, target.as_deref()).await?;

    if !resp.missing.is_empty() {
        eprintln!(
            "ferrvault: {} secret(s) not found: {}",
            resp.missing.len(),
            resp.missing.join(", ")
        );
    }

    if let Some(single) = name.as_deref() {
        if resp.hits.is_empty() {
            return Err(anyhow!("secret `{single}` not found"));
        }
        let value = &resp.hits[0].value;
        if matches!(format, OutputFormat::Value) {
            println!("{value}");
            return Ok(());
        }
    }

    emit(&resp.hits, format)?;
    Ok(())
}

fn emit(hits: &[BulkReadHit], format: OutputFormat) -> Result<()> {
    match format {
        OutputFormat::Value => {
            for h in hits {
                println!("{}", h.value);
            }
        }
        OutputFormat::Env => {
            for h in hits {
                println!("{}={}", h.name, shell_quote(&h.value));
            }
        }
        OutputFormat::Dotenv => {
            for h in hits {
                println!("{}={}", h.name, dotenv_quote(&h.value));
            }
        }
        OutputFormat::Json => {
            let map: BTreeMap<&str, &str> = hits
                .iter()
                .map(|h| (h.name.as_str(), h.value.as_str()))
                .collect();
            println!("{}", serde_json::to_string_pretty(&map)?);
        }
    }
    Ok(())
}

fn shell_quote(v: &str) -> String {
    let escaped = v.replace('\'', "'\\''");
    format!("'{escaped}'")
}

fn dotenv_quote(v: &str) -> String {
    if v.contains(['\n', '"', '\\']) {
        let escaped = v.replace('\\', "\\\\").replace('"', "\\\"");
        format!("\"{escaped}\"")
    } else {
        format!("\"{v}\"")
    }
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn shell_quotes_single_quotes() {
        assert_eq!(shell_quote("foo"), "'foo'");
        assert_eq!(shell_quote("can't"), "'can'\\''t'");
    }

    #[test]
    fn dotenv_quotes_newlines() {
        assert_eq!(dotenv_quote("simple"), "\"simple\"");
        assert_eq!(dotenv_quote("multi\nline"), "\"multi\nline\"");
        assert_eq!(dotenv_quote("has \"quote\""), "\"has \\\"quote\\\"\"");
    }
}
