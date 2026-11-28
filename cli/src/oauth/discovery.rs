use reqwest::{Client, Url, header::CONTENT_TYPE};
use serde::Deserialize;

use super::idp::IdpError;

const WELL_KNOWN: [&str; 2] = [".well-known", "oauth-authorization-server"];
const API_SEGMENT: &str = "api";

#[derive(Debug, Clone)]
pub struct Endpoints {
    pub authorization: Url,
    pub device_authorization: Url,
    pub token: Url,
}

#[derive(Debug)]
pub struct Discovered {
    pub issuer: Url,
    pub endpoints: Endpoints,
}

#[derive(Deserialize)]
struct Document {
    issuer: String,
    authorization_endpoint: String,
    device_authorization_endpoint: String,
    token_endpoint: String,
}

pub async fn discover(http: &Client, idp: &Url) -> Result<Option<Discovered>, IdpError> {
    let root = without_api(idp)?;
    let mut web_page = None;
    let candidates = [
        [WELL_KNOWN.as_slice(), &[API_SEGMENT]].concat(),
        WELL_KNOWN.to_vec(),
        [&[API_SEGMENT], WELL_KNOWN.as_slice()].concat(),
    ];
    for suffix in &candidates {
        let url = join(&root, suffix)?;
        let resp = http.get(url.clone()).send().await?;
        let status = resp.status();
        let html = is_html_content_type(resp.headers().get(CONTENT_TYPE));
        let body = resp.text().await?;
        if html || body.trim_start().starts_with('<') {
            web_page.get_or_insert((url, status.as_u16()));
            continue;
        }
        if status.is_server_error() {
            return Err(IdpError::Unavailable(status.as_u16()));
        }
        if status.is_success() {
            let document: Document =
                serde_json::from_str(&body).map_err(|e| IdpError::Malformed(e.to_string()))?;
            return document.resolve(idp).map(Some);
        }
    }
    match web_page {
        Some((url, status)) => Err(IdpError::NotAnApi {
            url: url.to_string(),
            status,
            suggestion: api_url(idp)?.to_string(),
        }),
        None => Ok(None),
    }
}

pub fn api_url(idp: &Url) -> Result<Url, IdpError> {
    join(&without_api(idp)?, &[API_SEGMENT])
}

pub fn is_html_content_type(value: Option<&reqwest::header::HeaderValue>) -> bool {
    value
        .and_then(|v| v.to_str().ok())
        .is_some_and(|v| v.to_ascii_lowercase().contains("html"))
}

impl Document {
    fn resolve(self, idp: &Url) -> Result<Discovered, IdpError> {
        let parse = |name: &str, raw: &str| -> Result<Url, IdpError> {
            let url = Url::parse(raw)
                .map_err(|e| IdpError::Malformed(format!("discovery {name} {raw}: {e}")))?;
            if idp.scheme() == "https" && url.scheme() != "https" {
                return Err(IdpError::Malformed(format!(
                    "discovery {name} {raw} is not https"
                )));
            }
            Ok(url)
        };
        Ok(Discovered {
            issuer: parse("issuer", &self.issuer)?,
            endpoints: Endpoints {
                authorization: parse("authorization_endpoint", &self.authorization_endpoint)?,
                device_authorization: parse(
                    "device_authorization_endpoint",
                    &self.device_authorization_endpoint,
                )?,
                token: parse("token_endpoint", &self.token_endpoint)?,
            },
        })
    }
}

fn without_api(idp: &Url) -> Result<Url, IdpError> {
    let mut root = idp.clone();
    let mut segments = root
        .path_segments_mut()
        .map_err(|()| IdpError::Malformed("identity provider URL cannot carry a path".into()))?;
    segments.pop_if_empty();
    drop(segments);
    if root.path_segments().and_then(Iterator::last) == Some(API_SEGMENT) {
        root.path_segments_mut()
            .map_err(|()| IdpError::Malformed("identity provider URL cannot carry a path".into()))?
            .pop();
    }
    Ok(root)
}

fn join(root: &Url, route: &[&str]) -> Result<Url, IdpError> {
    super::idp::endpoint(root, route)
}

#[cfg(test)]
mod tests {
    use serde_json::json;

    use super::*;
    use crate::testing::{MockResponse, MockServer, http_client};

    fn document(base: &Url, issuer_suffix: &str) -> serde_json::Value {
        let host = base.as_str().trim_end_matches('/');
        json!({
            "issuer": format!("{host}{issuer_suffix}"),
            "authorization_endpoint": format!("{host}/authorize"),
            "device_authorization_endpoint": format!("{host}/api/auth/device/code"),
            "token_endpoint": format!("{host}/api/auth/exchange"),
        })
    }

    #[test]
    fn a_trailing_api_segment_and_slash_are_stripped_once() {
        for raw in [
            "https://auth.example",
            "https://auth.example/",
            "https://auth.example/api",
            "https://auth.example/api/",
        ] {
            let root = without_api(&Url::parse(raw).unwrap()).unwrap();
            assert_eq!(root.path().trim_end_matches('/'), "", "{raw}");
        }
        let nested = without_api(&Url::parse("https://auth.example/api/api").unwrap()).unwrap();
        assert_eq!(nested.path(), "/api");
        let prefixed = without_api(&Url::parse("https://auth.example/t/api").unwrap()).unwrap();
        assert_eq!(prefixed.path(), "/t");
    }

    #[tokio::test]
    async fn the_api_document_is_preferred_and_gives_the_issuer_and_endpoints() {
        let server = MockServer::start(|r| match r.path.as_str() {
            "/.well-known/oauth-authorization-server/api" => {
                let base = Url::parse(&format!("http://{}", r.header("host").unwrap())).unwrap();
                MockResponse::json(200, &document(&base, "/api"))
            }
            other => panic!("unexpected {other}"),
        })
        .await;
        let found = discover(&http_client(), &server.url)
            .await
            .unwrap()
            .unwrap();
        assert_eq!(found.issuer.path(), "/api");
        assert_eq!(found.endpoints.token.path(), "/api/auth/exchange");
        assert_eq!(found.endpoints.authorization.path(), "/authorize");
        assert_eq!(
            found.endpoints.device_authorization.path(),
            "/api/auth/device/code"
        );
    }

    #[tokio::test]
    async fn the_plain_document_is_used_when_the_api_one_is_missing() {
        let server = MockServer::start(|r| match r.path.as_str() {
            "/.well-known/oauth-authorization-server" => {
                let base = Url::parse(&format!("http://{}", r.header("host").unwrap())).unwrap();
                MockResponse::json(200, &document(&base, ""))
            }
            _ => MockResponse::json(404, &json!({"error":"not_found"})),
        })
        .await;
        let found = discover(&http_client(), &server.url)
            .await
            .unwrap()
            .unwrap();
        assert_eq!(server.requests().len(), 2);
        assert_eq!(found.issuer.path(), "/");
    }

    #[tokio::test]
    async fn no_document_at_all_means_no_discovery() {
        let server = MockServer::start(|_| MockResponse::json(404, &json!({}))).await;
        assert!(
            discover(&http_client(), &server.url)
                .await
                .unwrap()
                .is_none()
        );
    }

    #[tokio::test]
    async fn html_answers_name_the_url_called_and_suggest_the_api_prefix() {
        let server = MockServer::start(|_| MockResponse::html(200, "<html>front end</html>")).await;
        let err = discover(&http_client(), &server.url)
            .await
            .unwrap_err()
            .to_string();
        assert!(
            err.contains("/.well-known/oauth-authorization-server/api"),
            "{err}"
        );
        assert!(err.contains(&format!("{}api", server.url)), "{err}");
    }

    #[test]
    fn an_https_provider_cannot_downgrade_its_endpoints_to_http() {
        let doc = |token: &str| Document {
            issuer: "https://auth.example/api".into(),
            authorization_endpoint: "https://auth.example/authorize".into(),
            device_authorization_endpoint: "https://auth.example/api/auth/device/code".into(),
            token_endpoint: token.into(),
        };
        let idp = Url::parse("https://auth.example").unwrap();
        let err = doc("http://auth.example/api/auth/exchange")
            .resolve(&idp)
            .unwrap_err()
            .to_string();
        assert!(
            err.contains("token_endpoint") && err.contains("not https"),
            "{err}"
        );
        let other_host = doc("https://api.example/auth/exchange")
            .resolve(&idp)
            .unwrap();
        assert_eq!(other_host.endpoints.token.host_str(), Some("api.example"));
    }

    #[tokio::test]
    async fn the_document_under_the_api_prefix_is_the_last_resort() {
        let server = MockServer::start(|r| match r.path.as_str() {
            "/api/.well-known/oauth-authorization-server" => {
                let base = Url::parse(&format!("http://{}", r.header("host").unwrap())).unwrap();
                MockResponse::json(200, &document(&base, "/api"))
            }
            _ => MockResponse::html(200, "<html>front end</html>"),
        })
        .await;
        let found = discover(&http_client(), &server.url)
            .await
            .unwrap()
            .unwrap();
        assert_eq!(found.issuer.path(), "/api");
        assert_eq!(server.requests().len(), 3);
    }

    #[tokio::test]
    async fn a_document_missing_an_endpoint_is_malformed() {
        let server = MockServer::start(|_| MockResponse::json(200, &json!({"issuer":"x"}))).await;
        assert!(matches!(
            discover(&http_client(), &server.url).await,
            Err(IdpError::Malformed(_))
        ));
    }
}
