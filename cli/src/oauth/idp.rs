use anyhow::Result;
use chrono::{DateTime, TimeDelta, Utc};
use reqwest::{Client, StatusCode, Url};
use serde::{Deserialize, Serialize};
use serde_json::json;
use thiserror::Error;
use zeroize::{Zeroize, ZeroizeOnDrop};

use super::CLIENT_ID;
use super::discovery::{Endpoints, api_url, discover, is_html_content_type};
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
    #[error(
        "{url} answered with a web page instead of JSON (status {status}); this looks like the web front end, not the identity API. Did you mean --idp-url {suggestion}?"
    )]
    NotAnApi {
        url: String,
        status: u16,
        suggestion: String,
    },
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
    #[serde(default = "default_access_ttl")]
    pub expires_in: i64,
    #[serde(default)]
    #[zeroize(skip)]
    pub user: Option<IdpUser>,
}

impl TokenGrant {
    pub fn expires_at(&self, now: DateTime<Utc>) -> Result<DateTime<Utc>, IdpError> {
        let malformed = || IdpError::Malformed(format!("invalid expires_in {}", self.expires_in));
        if self.expires_in < 0 {
            return Err(malformed());
        }
        let lifetime = TimeDelta::try_seconds(self.expires_in).ok_or_else(malformed)?;
        now.checked_add_signed(lifetime).ok_or_else(malformed)
    }
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

fn default_access_ttl() -> i64 {
    300
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
    discovered: Option<Endpoints>,
}

impl IdpClient {
    pub fn new(base: Url, tls: &TlsOptions<'_>) -> Result<Self> {
        Ok(Self {
            http: build_client(tls)?,
            base,
            discovered: None,
        })
    }

    pub async fn resolve(base: Url, tls: &TlsOptions<'_>) -> Result<Self> {
        Ok(Self::resolve_with(base, build_client(tls)?).await?)
    }

    pub async fn resolve_with(base: Url, http: Client) -> Result<Self, IdpError> {
        Ok(match discover(&http, &base).await? {
            Some(found) => Self {
                http,
                base: found.issuer,
                discovered: Some(found.endpoints),
            },
            None => Self {
                http,
                base,
                discovered: None,
            },
        })
    }

    #[cfg(test)]
    pub fn with_http(base: Url, http: Client) -> Self {
        Self {
            http,
            base,
            discovered: None,
        }
    }

    pub fn authorization_url(&self) -> Result<Url, IdpError> {
        match &self.discovered {
            Some(e) => Ok(e.authorization.clone()),
            None => endpoint(&self.base, &["authorize"]),
        }
    }

    fn token_url(&self) -> Result<Url, IdpError> {
        match &self.discovered {
            Some(e) => Ok(e.token.clone()),
            None => endpoint(&self.base, &["auth", "exchange"]),
        }
    }

    fn device_authorization_url(&self) -> Result<Url, IdpError> {
        match &self.discovered {
            Some(e) => Ok(e.device_authorization.clone()),
            None => endpoint(&self.base, &["auth", "device", "code"]),
        }
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
                self.token_url()?,
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
                endpoint(&self.base, &["auth", "refresh"])?,
                &json!({ "client_id": CLIENT_ID, "refresh_token": refresh_token }),
            )
            .await?;
        parse_grant(status, &body)
    }

    pub async fn logout(&self, access_token: &str) -> Result<(), IdpError> {
        let url = endpoint(&self.base, &["auth", "logout"])?;
        let resp = self.http.post(url).bearer_auth(access_token).send().await?;
        let status = resp.status();
        if status.is_success() {
            return Ok(());
        }
        let body = resp.text().await?;
        Err(classify_failure(status, &body))
    }

    pub async fn start_device(&self) -> Result<DeviceAuthorization, IdpError> {
        let (status, body) = self
            .post(
                self.device_authorization_url()?,
                &json!({ "client_id": CLIENT_ID }),
            )
            .await?;
        if !status.is_success() {
            return Err(classify_failure(status, &body));
        }
        serde_json::from_str(&body).map_err(|e| IdpError::Malformed(e.to_string()))
    }

    pub async fn poll_device(&self, device_code: &str) -> Result<DevicePoll, IdpError> {
        let (status, body) = self
            .post(
                endpoint(&self.base, &["auth", "device", "token"])?,
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
        url: Url,
        body: &serde_json::Value,
    ) -> Result<(StatusCode, String), IdpError> {
        let resp = self.http.post(url.clone()).json(body).send().await?;
        let status = resp.status();
        let html = is_html_content_type(resp.headers().get(reqwest::header::CONTENT_TYPE));
        let text = resp.text().await?;
        if html {
            return Err(IdpError::NotAnApi {
                url: url.to_string(),
                status: status.as_u16(),
                suggestion: api_url(&self.base)?.to_string(),
            });
        }
        Ok((status, text))
    }
}

pub fn endpoint(base: &Url, route: &[&str]) -> Result<Url, IdpError> {
    let mut url = base.clone();
    url.path_segments_mut()
        .map_err(|()| IdpError::Malformed("identity provider URL cannot carry a path".into()))?
        .pop_if_empty()
        .extend(route);
    Ok(url)
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
