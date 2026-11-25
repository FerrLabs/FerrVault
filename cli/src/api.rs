use std::path::Path;
use std::sync::Arc;

use anyhow::{Context, Result, anyhow};
use chrono::{DateTime, Utc};
use reqwest::{Client, Method, StatusCode, Url};
use serde::{Deserialize, Serialize};
use zeroize::Zeroizing;

use crate::session::{SessionAuth, SessionSummary};
use crate::tls::{TlsOptions, build_client};

pub enum Auth {
    Sat(Zeroizing<String>),
    Session(Arc<SessionAuth>),
}

pub struct ApiConfig {
    pub base_url: Url,
    pub auth: Auth,
}

const ORG_HEADER: &str = "x-ferrvault-org";
const ORGS_PATH: &str = "/user/orgs";
const VAULTS_PATH: &str = "/user/vaults";

#[derive(Debug, Clone, Deserialize)]
pub struct Choice {
    pub slug: String,
    #[serde(default)]
    pub name: Option<String>,
}

#[derive(Debug, Clone, Deserialize, Serialize)]
#[serde(rename_all = "lowercase")]
pub enum Role {
    Viewer,
    Writer,
    Admin,
}

#[derive(Debug, Clone, Deserialize, Serialize)]
pub struct OperatorMe {
    pub token_id: String,
    pub vault_id: String,
    pub vault_slug: String,
    pub role: Role,
    pub token_label: String,
    pub expires_at: Option<DateTime<Utc>>,
}

#[derive(Debug, Clone, Deserialize, Serialize)]
pub struct InventoryItem {
    pub name: String,
    pub version: i32,
    pub updated_at: DateTime<Utc>,
}

#[derive(Debug, Clone, Deserialize)]
pub struct BulkReadHit {
    pub name: String,
    #[serde(skip_serializing)]
    pub value: String,
    #[allow(dead_code)]
    pub version: i32,
    #[allow(dead_code)]
    pub fetched_at: DateTime<Utc>,
}

#[derive(Debug, Clone, Deserialize)]
pub struct BulkReadResponse {
    pub hits: Vec<BulkReadHit>,
    pub missing: Vec<String>,
}

pub struct ApiClient {
    cfg: ApiConfig,
    http: Client,
}

impl ApiClient {
    pub fn new(cfg: ApiConfig, tls: &TlsOptions<'_>) -> Result<Self> {
        let http = build_client(tls)?;
        Ok(Self { cfg, http })
    }

    #[cfg(test)]
    pub fn with_http(cfg: ApiConfig, http: Client) -> Self {
        Self { cfg, http }
    }

    pub async fn me(&self) -> Result<OperatorMe> {
        self.get_json("/operator/me").await
    }

    pub async fn list_orgs(&self) -> Result<Vec<Choice>> {
        self.get_json(ORGS_PATH).await
    }

    pub async fn list_vaults(&self) -> Result<Vec<Choice>> {
        self.get_json(VAULTS_PATH).await
    }

    pub async fn vault_slug(&self) -> Result<String> {
        match &self.cfg.auth {
            Auth::Sat(_) => Ok(self.me().await?.vault_slug),
            Auth::Session(s) => s
                .vault()
                .await
                .ok_or_else(|| anyhow!("no vault selected; run `ferrvault login --vault <slug>`")),
        }
    }

    pub async fn session_summary(&self) -> Option<SessionSummary> {
        match &self.cfg.auth {
            Auth::Sat(_) => None,
            Auth::Session(s) => Some(s.summary().await),
        }
    }

    pub async fn inventory(&self) -> Result<Vec<InventoryItem>> {
        self.get_json("/operator/secrets/list").await
    }

    pub async fn reveal(&self, vault: &str, names: Option<&[String]>) -> Result<BulkReadResponse> {
        let mut body = serde_json::json!({ "vault": vault });
        if let Some(names) = names {
            body["names"] = serde_json::json!(names);
        }
        let resp = self
            .send(Method::POST, "/operator/secrets/reveal", &[], Some(body))
            .await?;
        handle(resp).await
    }

    pub async fn create_secret(
        &self,
        vault: &str,
        name: &str,
        value: &str,
    ) -> Result<serde_json::Value> {
        let body = serde_json::json!({ "vault": vault, "name": name, "value": value });
        let resp = self
            .send(Method::POST, "/operator/secrets", &[], Some(body))
            .await?;
        handle(resp).await
    }

    pub async fn update_secret(
        &self,
        vault: &str,
        name: &str,
        value: &str,
    ) -> Result<serde_json::Value> {
        let body = serde_json::json!({ "vault": vault, "value": value });
        let resp = self
            .send(
                Method::PUT,
                &format!("/operator/secrets/{name}"),
                &[],
                Some(body),
            )
            .await?;
        handle(resp).await
    }

    pub async fn delete_secret(&self, vault: &str, name: &str) -> Result<()> {
        let resp = self
            .send(
                Method::DELETE,
                &format!("/operator/secrets/{name}"),
                &[("vault", vault)],
                None,
            )
            .await?;
        if resp.status().is_success() {
            return Ok(());
        }
        handle::<serde_json::Value>(resp).await.map(|_| ())
    }

    async fn get_json<T: serde::de::DeserializeOwned>(&self, path: &str) -> Result<T> {
        let resp = self.send(Method::GET, path, &[], None).await?;
        handle(resp).await
    }

    async fn send(
        &self,
        method: Method,
        path: &str,
        query: &[(&str, &str)],
        body: Option<serde_json::Value>,
    ) -> Result<reqwest::Response> {
        let mut url = self
            .cfg
            .base_url
            .join(path)
            .with_context(|| format!("constructing URL for {path}"))?;
        if !query.is_empty() {
            url.query_pairs_mut().extend_pairs(query);
        }
        let mut retried = false;
        loop {
            let (token, org) = self.credential().await?;
            let mut req = self
                .http
                .request(method.clone(), url.clone())
                .bearer_auth(token.as_str());
            if let Some(org) = org {
                req = req.header(ORG_HEADER, org);
            }
            if let Some(body) = &body {
                req = req.json(body);
            }
            let resp = req
                .send()
                .await
                .with_context(|| format!("{method} {path}"))?;
            if let (StatusCode::UNAUTHORIZED, Auth::Session(session), false) =
                (resp.status(), &self.cfg.auth, retried)
            {
                session.refresh_after_rejection(token.as_str()).await?;
                retried = true;
                continue;
            }
            return Ok(resp);
        }
    }

    async fn credential(&self) -> Result<(Zeroizing<String>, Option<String>)> {
        match &self.cfg.auth {
            Auth::Sat(token) => Ok((token.clone(), None)),
            Auth::Session(session) => {
                let c = session.credential().await?;
                Ok((c.token, c.org))
            }
        }
    }
}

async fn handle<T: serde::de::DeserializeOwned>(resp: reqwest::Response) -> Result<T> {
    let status = resp.status();
    if status.is_success() {
        return resp
            .json::<T>()
            .await
            .context("decoding JSON response body");
    }
    let retry_after = resp
        .headers()
        .get(reqwest::header::RETRY_AFTER)
        .and_then(|v| v.to_str().ok())
        .map(str::to_owned);
    let body = resp.text().await.unwrap_or_default();
    match status {
        StatusCode::UNAUTHORIZED => Err(anyhow!(
            "401 Unauthorized — credentials are missing, malformed, expired, or revoked. \
             Re-run `ferrvault login`."
        )),
        StatusCode::FORBIDDEN => Err(anyhow!(
            "403 Forbidden — your token does not have the required role on this vault. \
             Server said: {body}"
        )),
        StatusCode::NOT_FOUND => Err(anyhow!("404 Not Found — {body}")),
        StatusCode::TOO_MANY_REQUESTS => {
            let hint = retry_after
                .map(|s| format!(" (retry after {s}s)"))
                .unwrap_or_default();
            Err(anyhow!(
                "429 Too Many Requests — server rate-limited this SAT{hint}."
            ))
        }
        StatusCode::INTERNAL_SERVER_ERROR
        | StatusCode::BAD_GATEWAY
        | StatusCode::SERVICE_UNAVAILABLE
        | StatusCode::GATEWAY_TIMEOUT => Err(anyhow!(
            "{status} — upstream FerrVault is unhealthy. Server said: {body}"
        )),
        _ => Err(anyhow!("{status}: {body}")),
    }
}

pub struct TlsArgs<'a> {
    pub ca_cert: Option<&'a Path>,
    pub client_cert: Option<&'a Path>,
    pub client_key: Option<&'a Path>,
    pub pin_sha256: Option<&'a str>,
    pub insecure_skip_verify: bool,
}

impl<'a> From<TlsArgs<'a>> for TlsOptions<'a> {
    fn from(a: TlsArgs<'a>) -> Self {
        TlsOptions {
            ca_cert_path: a.ca_cert,
            client_cert_path: a.client_cert,
            client_key_path: a.client_key,
            pin_sha256_b64: a.pin_sha256,
            insecure_skip_verify: a.insecure_skip_verify,
        }
    }
}

#[cfg(test)]
mod tests {
    use std::sync::Arc;

    use chrono::{Duration, Utc};
    use serde_json::json;

    use super::*;
    use crate::oauth::idp::IdpClient;
    use crate::session::{MemorySessionStore, Session};
    use crate::testing::{MockResponse, MockServer};

    fn me_body() -> serde_json::Value {
        json!({"token_id":"t","vault_id":"v","vault_slug":"prod","role":"admin","token_label":"l","expires_at":null})
    }

    fn bearer(r: &crate::testing::Recorded) -> String {
        r.header("authorization").unwrap_or_default().to_owned()
    }

    async fn rotating_idp() -> MockServer {
        MockServer::start(|_| {
            MockResponse::json(
                200,
                &json!({"token":"new-access","refresh_token":"new-refresh","expires_in":900}),
            )
        })
        .await
    }

    fn session_client(
        api: &MockServer,
        idp: &MockServer,
        expires_in: i64,
        vault: Option<&str>,
    ) -> (ApiClient, Arc<MemorySessionStore>) {
        let store = Arc::new(MemorySessionStore::default());
        let session = Session {
            idp_url: idp.url.to_string(),
            access_token: Zeroizing::new("old-access".into()),
            refresh_token: Zeroizing::new("old-refresh".into()),
            expires_at: Utc::now() + Duration::seconds(expires_in),
            account: None,
            org: Some("acme".into()),
            vault: vault.map(str::to_owned),
        };
        let auth = SessionAuth::new(
            session,
            IdpClient::with_http(idp.url.clone(), crate::testing::http_client()),
            store.clone(),
        );
        let client = ApiClient::with_http(
            ApiConfig {
                base_url: api.url.clone(),
                auth: Auth::Session(Arc::new(auth)),
            },
            crate::testing::http_client(),
        );
        (client, store)
    }

    #[tokio::test]
    async fn sat_requests_are_unchanged_bearer_without_org_header() {
        let api = MockServer::start(|_| MockResponse::json(200, &me_body())).await;
        let client = ApiClient::with_http(
            ApiConfig {
                base_url: api.url.clone(),
                auth: Auth::Sat(Zeroizing::new("fvsat_abc".into())),
            },
            crate::testing::http_client(),
        );
        assert_eq!(client.vault_slug().await.unwrap(), "prod");
        let sent = api.requests();
        assert_eq!(sent.len(), 1);
        assert_eq!(sent[0].path, "/operator/me");
        assert_eq!(bearer(&sent[0]), "Bearer fvsat_abc");
        assert!(sent[0].header("x-ferrvault-org").is_none());
    }

    #[tokio::test]
    async fn sat_401_is_final_and_never_attempts_a_refresh() {
        let api = MockServer::start(|_| MockResponse::json(401, &json!({}))).await;
        let client = ApiClient::with_http(
            ApiConfig {
                base_url: api.url.clone(),
                auth: Auth::Sat(Zeroizing::new("fvsat_abc".into())),
            },
            crate::testing::http_client(),
        );
        let e = format!("{:#}", client.me().await.unwrap_err());
        assert!(e.contains("401"), "{e}");
        assert_eq!(api.requests().len(), 1);
    }

    #[tokio::test]
    async fn session_requests_carry_the_jwt_and_org_header() {
        let api = MockServer::start(|_| MockResponse::json(200, &json!([]))).await;
        let idp = rotating_idp().await;
        let (client, _) = session_client(&api, &idp, 900, Some("prod"));
        client.inventory().await.unwrap();
        let sent = api.requests();
        assert_eq!(bearer(&sent[0]), "Bearer old-access");
        assert_eq!(sent[0].header("x-ferrvault-org"), Some("acme"));
        assert!(idp.requests().is_empty());
    }

    #[tokio::test]
    async fn expired_session_refreshes_before_the_request_and_persists() {
        let api = MockServer::start(|_| MockResponse::json(200, &json!([]))).await;
        let idp = rotating_idp().await;
        let (client, store) = session_client(&api, &idp, -10, Some("prod"));
        client.inventory().await.unwrap();
        assert_eq!(bearer(&api.requests()[0]), "Bearer new-access");
        assert_eq!(store.saved().unwrap().refresh_token.as_str(), "new-refresh");
    }

    #[tokio::test]
    async fn a_401_refreshes_once_and_replays_the_request() {
        let api = MockServer::start(|r| {
            if bearer(r) == "Bearer new-access" {
                MockResponse::json(200, &json!([]))
            } else {
                MockResponse::json(401, &json!({}))
            }
        })
        .await;
        let idp = rotating_idp().await;
        let (client, store) = session_client(&api, &idp, 900, Some("prod"));
        client.inventory().await.unwrap();
        assert_eq!(api.requests().len(), 2);
        assert_eq!(idp.requests().len(), 1);
        assert_eq!(store.saved().unwrap().access_token.as_str(), "new-access");
    }

    #[tokio::test]
    async fn a_401_after_refresh_is_reported_without_looping() {
        let api = MockServer::start(|_| MockResponse::json(401, &json!({}))).await;
        let idp = rotating_idp().await;
        let (client, _) = session_client(&api, &idp, 900, Some("prod"));
        assert!(
            client
                .inventory()
                .await
                .unwrap_err()
                .to_string()
                .contains("401")
        );
        assert_eq!(api.requests().len(), 2);
        assert_eq!(idp.requests().len(), 1);
    }

    #[tokio::test]
    async fn session_without_a_vault_asks_for_one() {
        let api = MockServer::start(|_| MockResponse::json(200, &json!([]))).await;
        let idp = rotating_idp().await;
        let (client, _) = session_client(&api, &idp, 900, None);
        let err = client.vault_slug().await.unwrap_err().to_string();
        assert!(err.contains("--vault"), "{err}");
        assert!(api.requests().is_empty());
    }
}
