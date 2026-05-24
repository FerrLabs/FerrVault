use anyhow::{Context, Result, anyhow};
use reqwest::Url;
use zeroize::Zeroizing;

use crate::api::{ApiClient, ApiConfig, TlsArgs};
use crate::cli::{Cli, Command};
use crate::storage::CredentialStore;

mod delete;
mod exec;
mod get;
mod list;
mod login;
mod logout;
mod set;
mod whoami;

pub async fn dispatch(cli: Cli) -> Result<()> {
    if let Command::Login { url } = cli.command {
        return login::run(cli.api_url, url, cli.token).await;
    }
    if matches!(cli.command, Command::Logout) {
        return logout::run();
    }

    let client = build_client(&cli)?;
    match cli.command {
        Command::Login { .. } | Command::Logout => unreachable!(),
        Command::Whoami { json } => whoami::run(&client, json).await,
        Command::List { json } => list::run(&client, json).await,
        Command::Get {
            name,
            all,
            names,
            format,
        } => get::run(&client, name, all, names, format).await,
        Command::Exec {
            names,
            all,
            only,
            argv,
        } => exec::run(&client, names, all, only, argv).await,
        Command::Set {
            name,
            value,
            stdin,
            from_file,
            update,
        } => set::run(&client, name, value, stdin, from_file, update).await,
        Command::Delete { name, yes } => delete::run(&client, name, yes).await,
    }
}

fn resolve_url(flag: Option<&str>) -> Result<Url> {
    let raw = match flag {
        Some(s) => s.to_string(),
        None => CredentialStore::load_url()?.ok_or_else(|| {
            anyhow!(
                "no FerrVault URL configured — pass --api-url, set FERRVAULT_URL, or run `ferrvault login --url ...`"
            )
        })?,
    };
    let url = Url::parse(&raw).with_context(|| format!("parsing api-url {raw}"))?;
    if url.scheme() != "https" {
        return Err(anyhow!(
            "api-url must use https:// — refusing to send a SAT over plaintext"
        ));
    }
    Ok(url)
}

fn resolve_token(flag: Option<&str>) -> Result<Zeroizing<String>> {
    if let Some(t) = flag {
        return Ok(Zeroizing::new(t.to_owned()));
    }
    CredentialStore::load_token()?.ok_or_else(|| {
        anyhow!(
            "no token configured — pass --token, set FERRVAULT_TOKEN, or run `ferrvault login` first"
        )
    })
}

fn build_client(cli: &Cli) -> Result<ApiClient> {
    let url = resolve_url(cli.api_url.as_deref())?;
    let token = resolve_token(cli.token.as_deref())?;
    let tls = TlsArgs {
        ca_cert: cli.ca_cert.as_deref(),
        client_cert: cli.client_cert.as_deref(),
        client_key: cli.client_key.as_deref(),
        pin_sha256: cli.pin_sha256.as_deref(),
        insecure_skip_verify: cli.insecure_skip_verify,
    };
    let opts: crate::tls::TlsOptions<'_> = tls.into();
    ApiClient::new(
        ApiConfig {
            base_url: url,
            token,
        },
        &opts,
    )
}
