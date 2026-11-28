use std::io::{BufRead, IsTerminal};
use std::sync::Arc;

use anyhow::{Context, Result, anyhow};
use chrono::Utc;
use reqwest::Url;
use zeroize::Zeroizing;

use super::connection::{idp_tls_options, require_https, tls_args};
use super::logout::end_stored_session;
use super::select::pick;
use crate::api::{ApiClient, ApiConfig, Auth, TlsArgs};
use crate::cli::{Cli, LoginArgs};
use crate::oauth::DEFAULT_IDP_URL;
use crate::oauth::browser::{browser_available, open_in_browser};
use crate::oauth::device::device_login;
use crate::oauth::flow::{BrowserLoginError, browser_login};
use crate::oauth::idp::{DeviceAuthorization, IdpClient, TokenGrant};
use crate::session::{KeyringSessionStore, MemorySessionStore, Session, SessionAuth, SessionStore};
use crate::storage::{CredentialStore, unavailable_error};
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

fn without_browser(method: Method, browser: bool) -> Method {
    match method {
        Method::Browser if !browser => Method::Device,
        other => other,
    }
}

pub async fn run(args: &LoginArgs, cli: &Cli) -> Result<()> {
    run_with_probe(args, cli, CredentialStore::probe).await
}

async fn run_with_probe(
    args: &LoginArgs,
    cli: &Cli,
    probe: impl FnOnce() -> Result<()>,
) -> Result<()> {
    probe().map_err(|e| unavailable_error(&e, cfg!(target_os = "linux")))?;
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
    let browser = browser_available();
    let method = without_browser(
        choose_method(
            args.device,
            args.with_token || cli.token.is_some(),
            std::io::stdin().is_terminal(),
        ),
        browser,
    );
    if method == Method::Device && !args.device && !browser {
        eprintln!(
            "ferrvault: no browser available (no DISPLAY, WAYLAND_DISPLAY or BROWSER); using the device flow."
        );
    }
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

    end_stored_session(&tls).await;
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
    let idp = IdpClient::resolve(idp_url, &idp_tls_options(&tls)).await?;

    let grant = authenticate(&idp, device, open_in_browser, tokio::time::sleep).await?;
    let session = Session::from_grant(idp.base().as_str(), &grant, Utc::now())
        .context("reading the login response")?;
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
    select_or_sign_out(&client, &auth, args, input).await?;

    let session = auth.snapshot().await;
    CredentialStore::save_url(url.as_str())?;
    end_stored_session(&tls).await;
    KeyringSessionStore.save(&session)?;
    CredentialStore::delete_token()?;

    eprintln!(
        "ferrvault: logged in to {} as {} (org={}, vault={}, env={}).",
        url,
        session.account.as_deref().unwrap_or("unknown account"),
        session.org.as_deref().unwrap_or("-"),
        session.vault.as_deref().unwrap_or("-"),
        session.environment.as_deref().unwrap_or("-"),
    );
    Ok(())
}

async fn authenticate<S, F>(
    idp: &IdpClient,
    device: bool,
    open: impl FnOnce(&Url) -> Result<(), String>,
    sleep: S,
) -> Result<TokenGrant>
where
    S: Fn(std::time::Duration) -> F,
    F: std::future::Future<Output = ()>,
{
    if !device {
        match browser_login(idp, open).await {
            Ok(grant) => return Ok(grant),
            Err(BrowserLoginError::Failed(e)) => return Err(e),
            Err(e @ BrowserLoginError::NoBrowser(_)) => {
                eprintln!("ferrvault: {e}; using the device flow.");
            }
        }
    }
    device_login(idp, announce_device, sleep).await
}

async fn select_or_sign_out(
    client: &ApiClient,
    auth: &SessionAuth,
    args: &LoginArgs,
    input: Option<Box<dyn BufRead>>,
) -> Result<()> {
    let Err(e) = select_scope(client, auth, args, input).await else {
        return Ok(());
    };
    if let Err(out) = auth.sign_out().await {
        eprintln!("ferrvault: could not end the new session remotely ({out:#}).");
    }
    Err(e)
}

async fn select_scope(
    client: &ApiClient,
    auth: &SessionAuth,
    args: &LoginArgs,
    mut input: Option<Box<dyn BufRead>>,
) -> Result<()> {
    let orgs = client.list_orgs().await.context("reading memberships")?;
    let org = pick("org", &orgs, args.org.as_deref(), &mut input)?;
    auth.select_org(&org.slug).await;

    let vaults = client.list_vaults().await.context("listing vaults")?;
    let vault = pick("vault", &vaults, args.vault.as_deref(), &mut input)?;
    auth.select_vault(&vault.slug).await;

    let environments = client
        .list_environments(&vault.slug)
        .await
        .context("listing environments")?;
    let environment = pick("env", &environments, args.env.as_deref(), &mut input)?;
    auth.select_environment(&environment.slug).await;
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
    fn a_browser_login_without_a_browser_becomes_the_device_flow() {
        assert_eq!(without_browser(Method::Browser, false), Method::Device);
        assert_eq!(without_browser(Method::Browser, true), Method::Browser);
        assert_eq!(without_browser(Method::Sat, false), Method::Sat);
    }

    #[test]
    fn device_flag_selects_device_flow_with_or_without_a_terminal() {
        assert_eq!(choose_method(true, false, false), Method::Device);
        assert_eq!(choose_method(true, false, true), Method::Device);
    }

    mod scope {
        use serde_json::json;

        use super::super::*;
        use crate::oauth::idp::IdpClient;
        use crate::testing::{MockResponse, MockServer, http_client};

        fn args(vault: Option<&str>) -> LoginArgs {
            LoginArgs {
                url: None,
                device: false,
                with_token: false,
                idp_url: None,
                org: None,
                vault: vault.map(str::to_owned),
                env: None,
            }
        }

        async fn run_select(vault: Option<&str>) -> (Result<()>, Session, MockServer, MockServer) {
            let api = MockServer::start(|r| match r.path.as_str() {
                "/me" => MockResponse::json(
                    200,
                    &json!({"user_id":"u","handle":"h","org_id":null,"org_role":null,
                            "memberships":[{"org_id":"org-1","role":"owner"}]}),
                ),
                "/vaults" => MockResponse::json(
                    200,
                    &json!([{"slug":"a","name":"A"},{"slug":"b","name":"B"}]),
                ),
                "/vaults/b/environments" => {
                    MockResponse::json(200, &json!([{"slug":"prod","name":"Prod"}]))
                }
                other => panic!("unexpected {other}"),
            })
            .await;
            let idp = MockServer::start(|_| MockResponse::empty(204)).await;
            let session = Session {
                idp_url: idp.url.to_string(),
                access_token: Zeroizing::new("t".into()),
                refresh_token: Zeroizing::new("r".into()),
                expires_at: Utc::now() + chrono::Duration::seconds(900),
                account: None,
                org: None,
                vault: None,
                environment: None,
            };
            let auth = Arc::new(SessionAuth::new(
                session,
                IdpClient::with_http(idp.url.clone(), http_client()),
                Arc::new(MemorySessionStore::default()),
            ));
            let client = ApiClient::with_http(
                ApiConfig {
                    base_url: api.url.clone(),
                    auth: Auth::Session(auth.clone()),
                },
                http_client(),
            );
            let result = select_or_sign_out(&client, &auth, &args(vault), None).await;
            (result, auth.snapshot().await, api, idp)
        }

        #[tokio::test]
        async fn single_org_and_environment_are_picked_and_the_vault_flag_is_honoured() {
            let (result, session, api, idp) = run_select(Some("b")).await;
            result.unwrap();
            assert_eq!(session.org.as_deref(), Some("org-1"));
            assert_eq!(session.vault.as_deref(), Some("b"));
            assert_eq!(session.environment.as_deref(), Some("prod"));
            let vault_listing = &api.requests_to("/vaults")[0];
            assert_eq!(vault_listing.header("x-ferrvault-org"), Some("org-1"));
            assert!(idp.requests().is_empty());
        }

        #[tokio::test]
        async fn several_vaults_without_a_flag_or_terminal_fail_before_saving_a_scope() {
            let (result, session, _, idp) = run_select(None).await;
            assert!(result.unwrap_err().to_string().contains("--vault"));
            assert!(session.vault.is_none() && session.environment.is_none());
            let logout = idp.requests_to("/auth/logout");
            assert_eq!(logout.len(), 1);
            assert_eq!(logout[0].header("authorization"), Some("Bearer t"));
        }
    }

    mod flows {
        use clap::Parser;
        use serde_json::{Value, json};

        use super::super::*;
        use crate::cli::Command;
        use crate::oauth::idp::IdpError;
        use crate::testing::{MockResponse, MockServer, http_client};

        fn discovery(r: &crate::testing::Recorded) -> Value {
            let host = format!("http://{}", r.header("host").unwrap());
            json!({
                "issuer": format!("{host}/api"),
                "authorization_endpoint": format!("{host}/authorize"),
                "device_authorization_endpoint": format!("{host}/api/auth/device/code"),
                "token_endpoint": format!("{host}/api/auth/exchange"),
            })
        }

        async fn identity_behind_a_front_end() -> MockServer {
            MockServer::start(|r| match r.path.as_str() {
                "/.well-known/oauth-authorization-server/api" => {
                    MockResponse::json(200, &discovery(r))
                }
                "/api/auth/device/code" => MockResponse::json(
                    200,
                    &json!({
                        "device_code": "dev-1",
                        "user_code": "ABCD-EFGH",
                        "verification_uri": "https://auth.example/device",
                        "expires_in": 600,
                        "interval": 1,
                    }),
                ),
                "/api/auth/device/token" => MockResponse::json(
                    200,
                    &json!({"token":"access-1","refresh_token":"refresh-1","expires_in":900}),
                ),
                _ => MockResponse::html(405, "<html>405 Not Allowed</html>"),
            })
            .await
        }

        async fn resolve(idp: &str) -> Result<IdpClient, IdpError> {
            IdpClient::resolve_with(Url::parse(idp).unwrap(), http_client()).await
        }

        async fn no_sleep(_: std::time::Duration) {}

        #[tokio::test]
        async fn the_device_flow_works_without_api_in_the_idp_url() {
            let server = identity_behind_a_front_end().await;
            let idp = resolve(server.url.as_str()).await.unwrap();
            let grant = authenticate(&idp, true, |_| Ok(()), no_sleep)
                .await
                .unwrap();
            assert_eq!(grant.token, "access-1");
            assert_eq!(idp.base().path(), "/api");
            assert_eq!(server.requests_to("/api/auth/device/code").len(), 1);
            assert_eq!(server.requests_to("/auth/device/code").len(), 0);
        }

        #[tokio::test]
        async fn the_device_flow_works_with_api_and_trailing_slashes_in_the_idp_url() {
            let server = identity_behind_a_front_end().await;
            for suffix in ["api", "api/"] {
                let idp = resolve(&format!("{}{suffix}", server.url)).await.unwrap();
                let grant = authenticate(&idp, true, |_| Ok(()), no_sleep)
                    .await
                    .unwrap();
                assert_eq!(grant.token, "access-1");
                assert_eq!(idp.base().path(), "/api");
            }
            assert_eq!(server.requests_to("/auth/device/code").len(), 0);
        }

        #[tokio::test]
        async fn a_front_end_without_discovery_gives_the_actionable_error() {
            let server = MockServer::start(|_| MockResponse::html(405, "<html>no</html>")).await;
            let err = resolve(server.url.as_str())
                .await
                .err()
                .unwrap()
                .to_string();
            assert!(
                err.contains(".well-known/oauth-authorization-server"),
                "{err}"
            );
            assert!(
                err.contains(&format!("--idp-url {}api", server.url)),
                "{err}"
            );
        }

        #[tokio::test]
        async fn a_front_end_hiding_behind_a_missing_discovery_document_is_still_named() {
            let server = MockServer::start(|r| {
                if r.path.contains(".well-known") {
                    MockResponse::json(404, &json!({}))
                } else {
                    MockResponse::html(405, "<html>no</html>")
                }
            })
            .await;
            let idp = resolve(server.url.as_str()).await.unwrap();
            let err = authenticate(&idp, true, |_| Ok(()), no_sleep)
                .await
                .err()
                .unwrap()
                .to_string();
            assert!(err.contains("/auth/device/code"), "{err}");
            assert!(err.contains("--idp-url"), "{err}");
        }

        #[tokio::test]
        async fn a_browser_that_cannot_open_falls_back_to_the_device_flow() {
            let server = identity_behind_a_front_end().await;
            let idp = resolve(server.url.as_str()).await.unwrap();
            let grant = authenticate(
                &idp,
                false,
                |_| Err("xdg-open exited with 3".into()),
                no_sleep,
            )
            .await
            .unwrap();
            assert_eq!(grant.token, "access-1");
            assert_eq!(server.requests_to("/api/auth/device/code").len(), 1);
        }

        #[tokio::test]
        async fn a_keyring_that_fails_the_probe_stops_login_before_any_idp_request() {
            let server = identity_behind_a_front_end().await;
            let cli = Cli::parse_from([
                "ferrvault",
                "login",
                "--url",
                "https://api.example",
                "--device",
                "--idp-url",
                server.url.as_str(),
            ]);
            let Command::Login(args) = &cli.command else {
                unreachable!()
            };
            let err = run_with_probe(args, &cli, || Err(anyhow!("SS error")))
                .await
                .unwrap_err()
                .to_string();
            assert!(err.contains("SS error"), "{err}");
            assert!(err.contains("keyring"), "{err}");
            assert!(server.requests().is_empty());
        }
    }
}
