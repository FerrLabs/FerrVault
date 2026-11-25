use anyhow::Result;
use reqwest::{Client, StatusCode, Url};
use serde::{Deserialize, Serialize};
use serde_json::json;
use thiserror::Error;
use zeroize::{Zeroize, ZeroizeOnDrop};

use super::CLIENT_ID;
use crate::tls::{TlsOptions, build_client};

const DEVICE_GRANT_TYPE: &str = "urn:ietf:params:oauth:grant-type:device_code";

#[derive(Debug, Error)]
pub enum IdpError {
    #[error("identity provider unreachable: {0}")]
    Transport(#[from] reqwest::Error),
    #[error("identity provider rejected the request ({status}): {code}")]
    Rejected { status: u16, code: String },
    #[error("identity provider unavailable ({0})")]
    Unavailable(u16),
    #[error("unexpected identity provider response: {0}")]
    Malformed(String),
}

#[derive(Debug, Clone, Deserialize, Serialize)]
pub struct IdpUser {
    #[serde(default)]
    pub email: Option<String>,
    #[serde(default)]
    pub name: Option<String>,
}

#[derive(Deserialize, Zeroize, ZeroizeOnDrop)]
pub struct TokenGrant {
    pub token: String,
    pub refresh_token: String,
    pub expires_in: i64,
    #[serde(default)]
    #[zeroize(skip)]
    pub user: Option<IdpUser>,
}

#[derive(Debug, Clone, Deserialize)]
pub struct DeviceAuthorization {
    pub device_code: String,
    pub user_code: String,
    pub verification_uri: String,
    #[serde(default)]
    pub verification_uri_complete: Option<String>,
    pub expires_in: u64,
    #[serde(default = "default_interval")]
    pub interval: u64,
}

fn default_interval() -> u64 {
    5
}

pub enum DevicePoll {
    Granted(TokenGrant),
    Pending,
    SlowDown,
    Expired,
    Denied,
}

#[derive(Deserialize)]
struct ErrorBody {
    error: String,
}

pub struct IdpClient {
    http: Client,
    base: Url,
}

impl IdpClient {
    pub fn new(base: Url, tls: &TlsOptions<'_>) -> Result<Self> {
        Ok(Self {
            http: build_client(tls)?,
            base,
        })
    }

    #[cfg(test)]
    pub fn with_http(base: Url, http: Client) -> Self {
        Self { http, base }
    }

    pub fn base(&self) -> &Url {
        &self.base
    }

    pub async fn exchange(
        &self,
        code: &str,
        redirect_uri: &str,
        code_verifier: &str,
    ) -> Result<TokenGrant, IdpError> {
        let (status, body) = self
            .post(
                "/v1/auth/exchange",
                &json!({
                    "code": code,
                    "client_id": CLIENT_ID,
                    "redirect_uri": redirect_uri,
                    "code_verifier": code_verifier,
                }),
            )
            .await?;
        parse_grant(status, &body)
    }

    pub async fn refresh(&self, refresh_token: &str) -> Result<TokenGrant, IdpError> {
        let (status, body) = self
            .post(
                "/v1/auth/refresh",
                &json!({ "client_id": CLIENT_ID, "refresh_token": refresh_token }),
            )
            .await?;
        parse_grant(status, &body)
    }

    pub async fn revoke(&self, refresh_token: &str) -> Result<(), IdpError> {
        let (status, body) = self
            .post(
                "/v1/auth/revoke",
                &json!({ "client_id": CLIENT_ID, "refresh_token": refresh_token }),
            )
            .await?;
        if status.is_success() {
            return Ok(());
        }
        Err(classify_failure(status, &body))
    }

    pub async fn start_device(&self) -> Result<DeviceAuthorization, IdpError> {
        let (status, body) = self
            .post("/v1/auth/device/code", &json!({ "client_id": CLIENT_ID }))
            .await?;
        if !status.is_success() {
            return Err(classify_failure(status, &body));
        }
        serde_json::from_str(&body).map_err(|e| IdpError::Malformed(e.to_string()))
    }

    pub async fn poll_device(&self, device_code: &str) -> Result<DevicePoll, IdpError> {
        let (status, body) = self
            .post(
                "/v1/auth/device/token",
                &json!({
                    "client_id": CLIENT_ID,
                    "grant_type": DEVICE_GRANT_TYPE,
                    "device_code": device_code,
                }),
            )
            .await?;
        if status.is_success() {
            if let Ok(e) = serde_json::from_str::<ErrorBody>(&body) {
                return device_error(&e.error, status);
            }
            return parse_grant(status, &body).map(DevicePoll::Granted);
        }
        match serde_json::from_str::<ErrorBody>(&body) {
            Ok(e) => device_error(&e.error, status),
            Err(_) => Err(classify_failure(status, &body)),
        }
    }

    async fn post(
        &self,
        path: &str,
        body: &serde_json::Value,
    ) -> Result<(StatusCode, String), IdpError> {
        let url = self
            .base
            .join(path)
            .map_err(|e| IdpError::Malformed(e.to_string()))?;
        let resp = self.http.post(url).json(body).send().await?;
        let status = resp.status();
        let text = resp.text().await?;
        Ok((status, text))
    }
}

fn device_error(code: &str, status: StatusCode) -> Result<DevicePoll, IdpError> {
    match code {
        "authorization_pending" => Ok(DevicePoll::Pending),
        "slow_down" => Ok(DevicePoll::SlowDown),
        "expired_token" => Ok(DevicePoll::Expired),
        "access_denied" => Ok(DevicePoll::Denied),
        other => Err(IdpError::Rejected {
            status: status.as_u16(),
            code: other.to_owned(),
        }),
    }
}

fn parse_grant(status: StatusCode, body: &str) -> Result<TokenGrant, IdpError> {
    if !status.is_success() {
        return Err(classify_failure(status, body));
    }
    serde_json::from_str(body).map_err(|e| IdpError::Malformed(e.to_string()))
}

fn classify_failure(status: StatusCode, body: &str) -> IdpError {
    if status.is_server_error() {
        return IdpError::Unavailable(status.as_u16());
    }
    let code =
        serde_json::from_str::<ErrorBody>(body).map_or_else(|_| status.to_string(), |e| e.error);
    IdpError::Rejected {
        status: status.as_u16(),
        code,
    }
}
