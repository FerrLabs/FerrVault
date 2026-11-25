use std::io::{BufRead, IsTerminal};
use std::sync::Arc;

use anyhow::{Context, Result, anyhow};
use chrono::Utc;
use reqwest::Url;
use zeroize::Zeroizing;

use super::connection::{idp_tls_options, require_https, tls_args};
use super::select::pick;
use crate::api::{ApiClient, ApiConfig, Auth, TlsArgs};
use crate::cli::{Cli, LoginArgs};
use crate::oauth::DEFAULT_IDP_URL;
use crate::oauth::device::device_login;
use crate::oauth::flow::{browser_login, open_in_browser};
use crate::oauth::idp::{DeviceAuthorization, IdpClient};
use crate::session::{KeyringSessionStore, MemorySessionStore, Session, SessionAuth, SessionStore};
use crate::storage::CredentialStore;
use crate::tls::TlsOptions;

#[derive(Debug, PartialEq, Eq)]
enum Method {
    Sat,
    Device,
    Browser,
}

fn choose_method(device: bool, wants_sat: bool, stdin_tty: bool) -> Method {
    if device {
        Method::Device
    } else if wants_sat || !stdin_tty {
        Method::Sat
    } else {
        Method::Browser
    }
}

pub async fn run(args: &LoginArgs, cli: &Cli) -> Result<()> {
    let url_raw = args
        .url
        .clone()
        .or_else(|| cli.api_url.clone())
        .or_else(|| CredentialStore::load_url().ok().flatten())
        .ok_or_else(|| {
            anyhow!("pass --url <https://...> on the first login (or set FERRVAULT_URL).")
        })?;
    let url = require_https(&url_raw, "login URL")?;
    let tls = tls_args(cli)?;

    if args.device && cli.token.is_some() {
        return Err(anyhow!("--device cannot be combined with a token"));
    }
    let method = choose_method(
        args.device,
        args.with_token || cli.token.is_some(),
        std::io::stdin().is_terminal(),
    );
    match method {
        Method::Sat => sat_login(url, cli, tls).await,
        Method::Device => oauth_login(url, args, tls, true).await,
        Method::Browser => oauth_login(url, args, tls, false).await,
    }
}

async fn sat_login(url: Url, cli: &Cli, tls: TlsArgs<'_>) -> Result<()> {
    let token = match cli.token.as_deref() {
        Some(t) => Zeroizing::new(t.to_owned()),
        None => prompt_token()?,
    };
    if !token.starts_with("fvsat_") {
        return Err(anyhow!(
            "token does not look like a FerrVault SAT (expected prefix `fvsat_`)"
        ));
    }
    let opts: TlsOptions<'_> = tls.into();
    let client = ApiClient::new(
        ApiConfig {
            base_url: url.clone(),
            auth: Auth::Sat(token.clone()),
        },
        &opts,
    )?;
    let me = client
        .me()
        .await
        .context("validating token against /operator/me")?;

    CredentialStore::save_url(url.as_str())?;
    CredentialStore::save_token(&token)?;
    CredentialStore::delete_session()?;

    eprintln!(
        "ferrvault: logged in to {} as token `{}` (vault={}, role={:?}).",
        url, me.token_label, me.vault_slug, me.role
    );
    Ok(())
}

async fn oauth_login(url: Url, args: &LoginArgs, tls: TlsArgs<'_>, device: bool) -> Result<()> {
    let idp_raw = args.idp_url.as_deref().unwrap_or(DEFAULT_IDP_URL);
    let idp_url = require_https(idp_raw, "identity provider URL")?;
    let idp = IdpClient::new(idp_url.clone(), &idp_tls_options(&tls))?;

    let grant = if device {
        device_login(&idp, announce_device, tokio::time::sleep).await?
    } else {
        browser_login(&idp, open_in_browser).await?
    };
    let session = Session::from_grant(idp_url.as_str(), &grant, Utc::now());
    drop(grant);

    let scratch: Arc<dyn SessionStore> = Arc::new(MemorySessionStore::default());
    let auth = Arc::new(SessionAuth::new(session, idp, scratch));
    let opts: TlsOptions<'_> = tls.into();
    let client = ApiClient::new(
        ApiConfig {
            base_url: url.clone(),
            auth: Auth::Session(auth.clone()),
        },
        &opts,
    )?;

    let stdin = std::io::stdin();
    let input = stdin
        .is_terminal()
        .then(|| Box::new(stdin.lock()) as Box<dyn BufRead>);
    select_scope(&client, &auth, args, input).await?;

    let session = auth.snapshot().await;
    CredentialStore::save_url(url.as_str())?;
    KeyringSessionStore.save(&session)?;
    CredentialStore::delete_token()?;

    eprintln!(
        "ferrvault: logged in to {} as {} (org={}, vault={}).",
        url,
        session.account.as_deref().unwrap_or("unknown account"),
        session.org.as_deref().unwrap_or("-"),
        session.vault.as_deref().unwrap_or("-"),
    );
    Ok(())
}

async fn select_scope(
    client: &ApiClient,
    auth: &SessionAuth,
    args: &LoginArgs,
    mut input: Option<Box<dyn BufRead>>,
) -> Result<()> {
    let orgs = client.list_orgs().await.context("listing organizations")?;
    let org = pick("org", &orgs, args.org.as_deref(), &mut input)?;
    auth.select(Some(org.slug.clone()), None).await;

    let vaults = client.list_vaults().await.context("listing vaults")?;
    let vault = pick("vault", &vaults, args.vault.as_deref(), &mut input)?;
    auth.select(Some(org.slug.clone()), Some(vault.slug.clone()))
        .await;
    Ok(())
}

fn announce_device(auth: &DeviceAuthorization) {
    let target = auth
        .verification_uri_complete
        .as_deref()
        .unwrap_or(&auth.verification_uri);
    eprintln!(
        "ferrvault: on any device, open {target}\nferrvault: and confirm the code {}",
        auth.user_code
    );
}

fn prompt_token() -> Result<Zeroizing<String>> {
    let stdin = std::io::stdin();
    if stdin.is_terminal() {
        let raw = rpassword::prompt_password("ferrvault token: ").context("reading token")?;
        let trimmed = raw.trim().to_owned();
        if trimmed.is_empty() {
            return Err(anyhow!("empty token"));
        }
        return Ok(Zeroizing::new(trimmed));
    }
    let mut line = String::new();
    stdin.lock().read_line(&mut line).context("reading token")?;
    let trimmed = line.trim();
    if trimmed.is_empty() {
        return Err(anyhow!("empty token"));
    }
    Ok(Zeroizing::new(trimmed.to_owned()))
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn explicit_token_or_with_token_means_sat_login_even_on_a_terminal() {
        assert_eq!(choose_method(false, true, true), Method::Sat);
        assert_eq!(choose_method(false, true, false), Method::Sat);
    }

    #[test]
    fn piped_stdin_without_token_still_reads_a_sat() {
        assert_eq!(choose_method(false, false, false), Method::Sat);
    }

    #[test]
    fn terminal_without_token_opens_the_browser() {
        assert_eq!(choose_method(false, false, true), Method::Browser);
    }

    #[test]
    fn device_flag_selects_device_flow_with_or_without_a_terminal() {
        assert_eq!(choose_method(true, false, false), Method::Device);
        assert_eq!(choose_method(true, false, true), Method::Device);
    }
}
