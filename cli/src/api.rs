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

#[derive(Debug, Clone, Deserialize)]
pub struct Choice {
    pub slug: String,
    #[serde(default)]
    pub name: Option<String>,
    #[serde(skip)]
    pub detail: Option<String>,
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
    #[serde(alias = "current_version")]
    pub version: i32,
    pub updated_at: DateTime<Utc>,
}

#[derive(Debug, Clone, Deserialize)]
pub struct BulkReadHit {
    pub name: String,
    #[serde(skip_serializing)]
    pub value: String,
    #[allow(dead_code)]
    #[serde(alias = "current_version")]
    pub version: i32,
    #[allow(dead_code)]
    #[serde(alias = "updated_at")]
    pub fetched_at: DateTime<Utc>,
}

#[derive(Debug, Clone, Deserialize)]
pub struct BulkReadResponse {
    pub hits: Vec<BulkReadHit>,
    pub missing: Vec<String>,
}

const NO_BODY: Option<&()> = None;

#[derive(Serialize)]
struct RevealBody<'a> {
    vault: &'a str,
    #[serde(skip_serializing_if = "Option::is_none")]
    names: Option<&'a [String]>,
}

#[derive(Serialize)]
struct CreateBody<'a> {
    vault: &'a str,
    name: &'a str,
    value: &'a str,
}

#[derive(Serialize)]
struct UpdateBody<'a> {
    vault: &'a str,
    value: &'a str,
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
        self.get_json(&["operator", "me"]).await
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

    pub async fn describe_scope(&self, vault: &str) -> String {
        match &self.cfg.auth {
            Auth::Sat(_) => format!("vault `{vault}`"),
            Auth::Session(s) => match s.summary().await.environment {
                Some(env) => format!("vault `{vault}`, environment `{env}`"),
                None => format!("vault `{vault}`"),
            },
        }
    }

    pub async fn session_summary(&self) -> Option<SessionSummary> {
        match &self.cfg.auth {
            Auth::Sat(_) => None,
            Auth::Session(s) => Some(s.summary().await),
        }
    }

    pub async fn inventory(&self) -> Result<Vec<InventoryItem>> {
        match &self.cfg.auth {
            Auth::Sat(_) => self.get_json(&["operator", "secrets", "list"]).await,
            Auth::Session(s) => self.user_inventory(s).await,
        }
    }

    pub async fn reveal(&self, vault: &str, names: Option<&[String]>) -> Result<BulkReadResponse> {
        if let Auth::Session(s) = &self.cfg.auth {
            return self.user_reveal(s, names).await;
        }
        let resp = self
            .send(
                Method::POST,
                &["operator", "secrets", "reveal"],
                &[],
                Some(&RevealBody { vault, names }),
            )
            .await?;
        handle(resp).await
    }

    pub async fn create_secret(
        &self,
        vault: &str,
        name: &str,
        value: &str,
    ) -> Result<serde_json::Value> {
        if let Auth::Session(s) = &self.cfg.auth {
            return self.user_create(s, name, value).await;
        }
        let resp = self
            .send(
                Method::POST,
                &["operator", "secrets"],
                &[],
                Some(&CreateBody { vault, name, value }),
            )
            .await?;
        handle(resp).await
    }

    pub async fn update_secret(
        &self,
        vault: &str,
        name: &str,
        value: &str,
    ) -> Result<serde_json::Value> {
        if let Auth::Session(s) = &self.cfg.auth {
            return self.user_update(s, name, value).await;
        }
        let resp = self
            .send(
                Method::PUT,
                &["operator", "secrets", name],
                &[],
                Some(&UpdateBody { vault, value }),
            )
            .await?;
        handle(resp).await
    }

    pub async fn delete_secret(&self, vault: &str, name: &str) -> Result<()> {
        if let Auth::Session(s) = &self.cfg.auth {
            return self.user_delete(s, name).await;
        }
        let resp = self
            .send(
                Method::DELETE,
                &["operator", "secrets", name],
                &[("vault", vault)],
                NO_BODY,
            )
            .await?;
        if resp.status().is_success() {
            return Ok(());
        }
        handle::<serde_json::Value>(resp).await.map(|_| ())
    }

    async fn get_json<T: serde::de::DeserializeOwned>(&self, route: &[&str]) -> Result<T> {
        let resp = self.send(Method::GET, route, &[], NO_BODY).await?;
        handle(resp).await
    }

    async fn send<B: Serialize + ?Sized>(
        &self,
        method: Method,
        route: &[&str],
        query: &[(&str, &str)],
        body: Option<&B>,
    ) -> Result<reqwest::Response> {
        let url = self.build_url(route, query)?;
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
            if let Some(body) = body {
                req = req.json(body);
            }
            let resp = req
                .send()
                .await
                .with_context(|| format!("{method} {}", url.path()))?;
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

    fn build_url(&self, route: &[&str], query: &[(&str, &str)]) -> Result<Url> {
        if let Some(bad) = route.iter().find(|s| is_unsafe_segment(s)) {
            return Err(anyhow!("{bad:?} is not a valid name"));
        }
        let mut url = self.cfg.base_url.clone();
        url.path_segments_mut()
            .map_err(|()| anyhow!("api-url cannot carry a path"))?
            .clear()
            .extend(route);
        if !query.is_empty() {
            url.query_pairs_mut().extend_pairs(query);
        }
        Ok(url)
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

fn is_unsafe_segment(segment: &str) -> bool {
    matches!(segment, "" | "." | "..") || segment.contains(['\t', '\n', '\r'])
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
            "403 Forbidden — your credentials do not have the required role on this vault. \
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

#[derive(Clone, Copy)]
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

mod user;

#[cfg(test)]
mod tests;
