use std::sync::Arc;

use anyhow::{Context, Result, anyhow};
use reqwest::Url;
use zeroize::Zeroizing;

use crate::api::{ApiClient, ApiConfig, Auth, TlsArgs};
use crate::cli::Cli;
use crate::oauth::idp::IdpClient;
use crate::session::{KeyringSessionStore, Session, SessionAuth};
use crate::storage::CredentialStore;
use crate::tls::TlsOptions;

const INSECURE_GATE_ENV: &str = "FERRVAULT_INSECURE_I_KNOW_WHAT_I_AM_DOING";

pub fn require_https(raw: &str, what: &str) -> Result<Url> {
    let url = Url::parse(raw).with_context(|| format!("parsing {what} {raw}"))?;
    if url.scheme() != "https" {
        return Err(anyhow!(
            "{what} must use https:// — refusing to send credentials over plaintext"
        ));
    }
    Ok(url)
}

pub fn resolve_url(flag: Option<&str>) -> Result<Url> {
    let raw = match flag {
        Some(s) => s.to_string(),
        None => CredentialStore::load_url()?.ok_or_else(|| {
            anyhow!(
                "no FerrVault URL configured — pass --api-url, set FERRVAULT_URL, or run `ferrvault login --url ...`"
            )
        })?,
    };
    require_https(&raw, "api-url")
}

pub fn check_insecure_gate(insecure: bool, gate_value: Option<&str>) -> Result<()> {
    if insecure && gate_value != Some("yes") {
        return Err(anyhow!(
            "--insecure-skip-verify refuses to run without {INSECURE_GATE_ENV}=yes in env. \
             This is a secrets CLI; if you really mean to skip TLS validation, set the env \
             var explicitly so a forgotten dev flag can't leak a token over an MITM."
        ));
    }
    Ok(())
}

pub fn tls_args(cli: &Cli) -> Result<TlsArgs<'_>> {
    check_insecure_gate(
        cli.insecure_skip_verify,
        std::env::var(INSECURE_GATE_ENV).ok().as_deref(),
    )?;
    Ok(TlsArgs {
        ca_cert: cli.ca_cert.as_deref(),
        client_cert: cli.client_cert.as_deref(),
        client_key: cli.client_key.as_deref(),
        pin_sha256: cli.pin_sha256.as_deref(),
        insecure_skip_verify: cli.insecure_skip_verify,
    })
}

pub fn idp_tls_options<'a>(args: &TlsArgs<'a>) -> TlsOptions<'a> {
    TlsOptions {
        ca_cert_path: args.ca_cert,
        client_cert_path: None,
        client_key_path: None,
        pin_sha256_b64: None,
        insecure_skip_verify: args.insecure_skip_verify,
    }
}

pub fn idp_client(session: &Session, tls: &TlsArgs<'_>) -> Result<IdpClient> {
    let base = require_https(&session.idp_url, "identity provider URL")?;
    IdpClient::new(base, &idp_tls_options(tls))
}

fn resolve_auth(cli: &Cli, tls: &TlsArgs<'_>) -> Result<Auth> {
    if let Some(t) = cli.token.as_deref() {
        return Ok(Auth::Sat(Zeroizing::new(t.to_owned())));
    }
    if let Some(t) = CredentialStore::load_token()? {
        return Ok(Auth::Sat(t));
    }
    if let Some(session) = CredentialStore::load_session()? {
        let idp = idp_client(&session, tls)?;
        let auth = SessionAuth::new(session, idp, Arc::new(KeyringSessionStore));
        return Ok(Auth::Session(Arc::new(auth)));
    }
    Err(anyhow!(
        "no credentials configured — pass --token, set FERRVAULT_TOKEN, or run `ferrvault login` first"
    ))
}

pub fn build_client(cli: &Cli) -> Result<ApiClient> {
    let url = resolve_url(cli.api_url.as_deref())?;
    let tls = tls_args(cli)?;
    let auth = resolve_auth(cli, &tls)?;
    let opts: TlsOptions<'_> = tls.into();
    ApiClient::new(
        ApiConfig {
            base_url: url,
            auth,
        },
        &opts,
    )
}

#[cfg(test)]
mod tests {
    use clap::Parser;

    use super::*;

    #[test]
    fn insecure_flag_needs_the_env_gate() {
        assert!(check_insecure_gate(true, None).is_err());
        assert!(check_insecure_gate(true, Some("1")).is_err());
        assert!(check_insecure_gate(true, Some("yes")).is_ok());
        assert!(check_insecure_gate(false, None).is_ok());
    }

    #[test]
    fn login_receives_the_global_tls_flags() {
        let cli = Cli::try_parse_from([
            "ferrvault",
            "--ca-cert",
            "/tmp/ca.pem",
            "--client-cert",
            "/tmp/c.pem",
            "--client-key",
            "/tmp/k.pem",
            "--pin-sha256",
            "abc",
            "login",
            "--url",
            "https://v.example",
        ])
        .unwrap();
        let args = tls_args(&cli).unwrap();
        assert_eq!(args.pin_sha256, Some("abc"));
        assert!(args.ca_cert.is_some());
        assert!(args.client_cert.is_some());
        assert!(args.client_key.is_some());
    }

    #[test]
    fn idp_client_keeps_trust_flags_but_never_the_vault_pin_or_client_cert() {
        let cli = Cli::try_parse_from([
            "ferrvault",
            "--ca-cert",
            "/tmp/ca.pem",
            "--client-cert",
            "/tmp/c.pem",
            "--pin-sha256",
            "abc",
            "logout",
        ])
        .unwrap();
        let opts = idp_tls_options(&tls_args(&cli).unwrap());
        assert!(opts.ca_cert_path.is_some());
        assert!(opts.pin_sha256_b64.is_none());
        assert!(opts.client_cert_path.is_none());
    }

    #[test]
    fn plaintext_urls_are_refused() {
        assert!(require_https("http://v.example", "api-url").is_err());
        assert!(require_https("https://v.example", "api-url").is_ok());
    }
}
