use std::time::Duration;

use anyhow::Context;
use reqwest::Url;
use thiserror::Error;

use super::CLIENT_ID;
use super::idp::{IdpClient, TokenGrant};
use super::loopback::CallbackServer;
use super::pkce::{Pkce, random_state};

const CALLBACK_TIMEOUT: Duration = Duration::from_secs(300);

#[derive(Debug, Error)]
pub enum BrowserLoginError {
    #[error("could not open a browser ({0})")]
    NoBrowser(String),
    #[error(transparent)]
    Failed(#[from] anyhow::Error),
}

pub async fn browser_login(
    idp: &IdpClient,
    open: impl FnOnce(&Url) -> Result<(), String>,
) -> Result<TokenGrant, BrowserLoginError> {
    let server = CallbackServer::bind().await?;
    let redirect_uri = server.redirect_uri();
    let pkce = Pkce::generate()?;
    let state = random_state()?;
    let url = authorize_url(
        &idp.authorization_url().context("building authorize URL")?,
        &redirect_uri,
        pkce.challenge(),
        &state,
    );
    open(&url).map_err(BrowserLoginError::NoBrowser)?;
    let code = server.wait_for_code(&state, CALLBACK_TIMEOUT).await?;
    let grant = idp
        .exchange(&code, &redirect_uri, pkce.verifier())
        .await
        .context("exchanging the authorization code")?;
    Ok(grant)
}

pub fn authorize_url(endpoint: &Url, redirect_uri: &str, challenge: &str, state: &str) -> Url {
    let mut url = endpoint.clone();
    url.query_pairs_mut()
        .append_pair("client_id", CLIENT_ID)
        .append_pair("redirect_uri", redirect_uri)
        .append_pair("code_challenge", challenge)
        .append_pair("code_challenge_method", "S256")
        .append_pair("state", state);
    url
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

    #[test]
    fn a_path_prefix_on_the_idp_url_is_kept() {
        let client = |raw: &str| {
            IdpClient::with_http(Url::parse(raw).unwrap(), crate::testing::http_client())
        };
        let url = client("https://host.example/tenant")
            .authorization_url()
            .unwrap();
        assert_eq!(url.path(), "/tenant/authorize");
        let trailing = client("https://host.example/tenant/")
            .authorization_url()
            .unwrap();
        assert_eq!(trailing.path(), "/tenant/authorize");
        let root = client("https://auth.ferrlabs.com")
            .authorization_url()
            .unwrap();
        assert_eq!(root.path(), "/authorize");
        let built = authorize_url(&url, "http://127.0.0.1:1/callback", "c", "s");
        assert_eq!(built.path(), "/tenant/authorize");
    }

    fn query(url: &Url, key: &str) -> String {
        url.query_pairs()
            .find(|(k, _)| k == key)
            .map(|(_, v)| v.into_owned())
            .unwrap()
    }

    fn call_back(url: &Url, code: &str, override_state: Option<&str>) {
        let redirect = query(url, "redirect_uri");
        let state = override_state.map_or_else(|| query(url, "state"), str::to_owned);
        let code = code.to_owned();
        tokio::spawn(async move {
            let _ = reqwest::get(format!("{redirect}?code={code}&state={state}")).await;
        });
    }

    #[tokio::test]
    async fn exchanges_the_code_with_the_verifier_matching_the_sent_challenge() {
        let idp = idp_granting().await;
        let mut seen = None;
        let grant = browser_login(&idp_for(&idp), |url| {
            seen = Some(url.clone());
            call_back(url, "the-code", None);
            Ok(())
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
    async fn a_forged_callback_never_reaches_the_exchange() {
        let idp = idp_granting().await;
        browser_login(&idp_for(&idp), |url| {
            call_back(url, "attacker-code", Some("attacker-state"));
            call_back(url, "the-code", None);
            Ok(())
        })
        .await
        .unwrap();
        let sent = idp.requests_to("/auth/exchange");
        assert_eq!(sent.len(), 1);
        assert_eq!(sent[0].json()["code"], "the-code");
    }

    #[tokio::test]
    async fn rejected_exchange_is_an_error() {
        let idp =
            MockServer::start(|_| MockResponse::json(400, &json!({"error":"invalid_grant"}))).await;
        let err = format!(
            "{:#}",
            browser_login(&idp_for(&idp), |url| {
                call_back(url, "the-code", None);
                Ok(())
            })
            .await
            .err()
            .unwrap()
        );
        assert!(err.contains("invalid_grant"), "{err}");
    }
}

#[cfg(test)]
mod launch_tests {
    use serde_json::json;

    use super::*;
    use crate::testing::{MockResponse, MockServer};

    #[tokio::test]
    async fn a_failed_launch_is_reported_before_waiting_for_a_callback() {
        let idp = MockServer::start(|_| MockResponse::json(200, &json!({}))).await;
        let client = IdpClient::with_http(idp.url.clone(), crate::testing::http_client());
        let err = browser_login(&client, |_| Err("xdg-open exited with 3".into()))
            .await
            .err()
            .unwrap();
        assert!(matches!(err, BrowserLoginError::NoBrowser(m) if m.contains("xdg-open")));
        assert!(idp.requests().is_empty());
    }
}
