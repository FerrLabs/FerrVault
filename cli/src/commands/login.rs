use std::io::{BufRead, IsTerminal, Write};

use anyhow::{Context, Result, anyhow};
use reqwest::Url;
use zeroize::Zeroizing;

use crate::api::{ApiClient, ApiConfig, TlsArgs};
use crate::storage::CredentialStore;

pub async fn run(
    api_url_flag: Option<String>,
    url_subarg: Option<String>,
    token_flag: Option<String>,
) -> Result<()> {
    let url_raw = url_subarg
        .or(api_url_flag)
        .or_else(|| CredentialStore::load_url().ok().flatten())
        .ok_or_else(|| {
            anyhow!("pass --url <https://...> on the first login (or set FERRVAULT_URL).")
        })?;
    let url = Url::parse(&url_raw).with_context(|| format!("parsing url {url_raw}"))?;
    if url.scheme() != "https" {
        return Err(anyhow!("login URL must use https://"));
    }

    let token = match token_flag {
        Some(t) => Zeroizing::new(t),
        None => prompt_token()?,
    };

    if !token.starts_with("fvsat_") {
        return Err(anyhow!(
            "token does not look like a FerrVault SAT (expected prefix `fvsat_`)"
        ));
    }

    let tls: crate::tls::TlsOptions<'_> = TlsArgs {
        ca_cert: None,
        client_cert: None,
        client_key: None,
        pin_sha256: None,
        insecure_skip_verify: false,
    }
    .into();
    let client = ApiClient::new(
        ApiConfig {
            base_url: url.clone(),
            token: token.clone(),
        },
        &tls,
    )?;
    let me = client
        .me()
        .await
        .context("validating token against /v1/operator/me")?;

    CredentialStore::save_url(url.as_str())?;
    CredentialStore::save_token(&token)?;

    eprintln!(
        "ferrvault: logged in to {} as token `{}` (vault={}, role={:?}).",
        url, me.token_label, me.vault_slug, me.role
    );
    Ok(())
}

fn prompt_token() -> Result<Zeroizing<String>> {
    let stdin = std::io::stdin();
    if stdin.is_terminal() {
        eprint!("ferrvault token (input hidden if your terminal supports it): ");
        std::io::stderr().flush().ok();
    }
    let mut line = String::new();
    stdin.lock().read_line(&mut line).context("reading token")?;
    let trimmed = line.trim();
    if trimmed.is_empty() {
        return Err(anyhow!("empty token"));
    }
    Ok(Zeroizing::new(trimmed.to_owned()))
}
