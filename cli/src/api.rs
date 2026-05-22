use std::path::Path;

use anyhow::{Context, Result, anyhow};
use chrono::{DateTime, Utc};
use reqwest::{Client, StatusCode, Url};
use serde::{Deserialize, Serialize};
use zeroize::Zeroizing;

use crate::tls::{TlsOptions, build_client};

#[derive(Debug, Clone)]
pub struct ApiConfig {
    pub base_url: Url,
    pub token: Zeroizing<String>,
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

#[derive(Debug, Serialize)]
struct RevealBody<'a> {
    vault: &'a str,
    #[serde(skip_serializing_if = "Option::is_none")]
    names: Option<&'a [String]>,
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

    pub async fn me(&self) -> Result<OperatorMe> {
        self.get_json("/v1/operator/me").await
    }

    pub async fn inventory(&self) -> Result<Vec<InventoryItem>> {
        self.get_json("/v1/operator/secrets/list").await
    }

    pub async fn reveal(&self, vault: &str, names: Option<&[String]>) -> Result<BulkReadResponse> {
        let url = self
            .cfg
            .base_url
            .join("/v1/operator/secrets/reveal")
            .context("constructing reveal URL")?;
        let body = RevealBody { vault, names };
        let resp = self
            .http
            .post(url)
            .bearer_auth(self.cfg.token.as_str())
            .json(&body)
            .send()
            .await
            .context("POST /v1/operator/secrets/reveal")?;
        handle(resp).await
    }

    async fn get_json<T: serde::de::DeserializeOwned>(&self, path: &str) -> Result<T> {
        let url = self
            .cfg
            .base_url
            .join(path)
            .with_context(|| format!("constructing URL for {path}"))?;
        let resp = self
            .http
            .get(url)
            .bearer_auth(self.cfg.token.as_str())
            .send()
            .await
            .with_context(|| format!("GET {path}"))?;
        handle(resp).await
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
            "401 Unauthorized — token is missing, malformed, expired, or revoked. \
             Re-run `ferrvault login` with a fresh token."
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
