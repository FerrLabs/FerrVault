use std::time::Duration;

use anyhow::{Context, Result};
use reqwest::Url;

use super::CLIENT_ID;
use super::idp::{IdpClient, TokenGrant};
use super::loopback::CallbackServer;
use super::pkce::{Pkce, random_state};

const CALLBACK_TIMEOUT: Duration = Duration::from_secs(300);
const AUTHORIZE_PATH: &str = "/authorize";

pub async fn browser_login(idp: &IdpClient, open: impl FnOnce(&Url)) -> Result<TokenGrant> {
    let server = CallbackServer::bind().await?;
    let redirect_uri = server.redirect_uri();
    let pkce = Pkce::generate()?;
    let state = random_state()?;
    let url = authorize_url(idp.base(), &redirect_uri, pkce.challenge(), &state)?;
    open(&url);
    let code = server.wait_for_code(&state, CALLBACK_TIMEOUT).await?;
    idp.exchange(&code, &redirect_uri, pkce.verifier())
        .await
        .context("exchanging the authorization code")
}

pub fn authorize_url(base: &Url, redirect_uri: &str, challenge: &str, state: &str) -> Result<Url> {
    let mut url = base
        .join(AUTHORIZE_PATH)
        .context("building authorize URL")?;
    url.query_pairs_mut()
        .append_pair("client_id", CLIENT_ID)
        .append_pair("redirect_uri", redirect_uri)
        .append_pair("code_challenge", challenge)
        .append_pair("code_challenge_method", "S256")
        .append_pair("state", state);
    Ok(url)
}

pub fn open_in_browser(url: &Url) {
    eprintln!("ferrvault: opening your browser to log in. If it does not open, visit:\n{url}");
    let launched = if cfg!(target_os = "macos") {
        std::process::Command::new("open").arg(url.as_str()).spawn()
    } else if cfg!(target_os = "windows") {
        std::process::Command::new("rundll32")
            .args(["url.dll,FileProtocolHandler", url.as_str()])
            .spawn()
    } else {
        std::process::Command::new("xdg-open")
            .arg(url.as_str())
            .spawn()
    };
    if launched.is_err() {
        eprintln!("ferrvault: could not launch a browser; open the URL above manually.");
    }
}

#[cfg(test)]
mod tests {
    use serde_json::json;

    use super::*;
    use crate::oauth::pkce::challenge_for;
    use crate::testing::{MockResponse, MockServer};

    fn idp_for(server: &MockServer) -> IdpClient {
        IdpClient::with_http(server.url.clone(), crate::testing::http_client())
    }

    async fn idp_granting() -> MockServer {
        MockServer::start(|r| {
            assert_eq!(r.path, "/auth/exchange");
            MockResponse::json(
                200,
                &json!({
                    "token": "access-1",
                    "refresh_token": "refresh-1",
                    "expires_in": 900,
                    "user": {"email": "dev@example.com"}
                }),
            )
        })
        .await
    }

    fn query(url: &Url, key: &str) -> String {
        url.query_pairs()
            .find(|(k, _)| k == key)
            .map(|(_, v)| v.into_owned())
            .unwrap()
    }

    fn call_back(url: &Url, override_state: Option<&str>) {
        let redirect = query(url, "redirect_uri");
        let state = override_state.map_or_else(|| query(url, "state"), str::to_owned);
        tokio::spawn(async move {
            let _ = reqwest::get(format!("{redirect}?code=the-code&state={state}")).await;
        });
    }

    #[tokio::test]
    async fn exchanges_the_code_with_the_verifier_matching_the_sent_challenge() {
        let idp = idp_granting().await;
        let mut seen = None;
        let grant = browser_login(&idp_for(&idp), |url| {
            seen = Some(url.clone());
            call_back(url, None);
        })
        .await
        .unwrap();
        assert_eq!(grant.token, "access-1");
        assert_eq!(grant.refresh_token, "refresh-1");

        let url = seen.unwrap();
        assert_eq!(url.path(), "/authorize");
        assert_eq!(query(&url, "client_id"), "ferrvault-cli");
        assert_eq!(query(&url, "code_challenge_method"), "S256");
        assert!(query(&url, "redirect_uri").starts_with("http://127.0.0.1:"));

        let sent = idp.requests_to("/auth/exchange");
        assert_eq!(sent.len(), 1);
        let body = sent[0].json();
        assert_eq!(body["code"], "the-code");
        assert_eq!(body["client_id"], "ferrvault-cli");
        assert_eq!(body["redirect_uri"], query(&url, "redirect_uri"));
        assert_eq!(
            challenge_for(body["code_verifier"].as_str().unwrap()),
            query(&url, "code_challenge")
        );
    }

    #[tokio::test]
    async fn state_mismatch_never_reaches_the_exchange() {
        let idp = idp_granting().await;
        let result = browser_login(&idp_for(&idp), |url| {
            call_back(url, Some("attacker-state"));
        })
        .await;
        assert!(result.is_err());
        assert!(idp.requests().is_empty());
    }

    #[tokio::test]
    async fn rejected_exchange_is_an_error() {
        let idp =
            MockServer::start(|_| MockResponse::json(400, &json!({"error":"invalid_grant"}))).await;
        let err = format!(
            "{:#}",
            browser_login(&idp_for(&idp), |url| {
                call_back(url, None);
            })
            .await
            .err()
            .unwrap()
        );
        assert!(err.contains("invalid_grant"), "{err}");
    }
}
