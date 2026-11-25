use std::time::Duration;

use anyhow::{Context, Result, anyhow};
use reqwest::Url;
use tokio::io::{AsyncReadExt, AsyncWriteExt};
use tokio::net::{TcpListener, TcpStream};

const CALLBACK_PATH: &str = "/callback";
const MAX_REQUEST_BYTES: usize = 16 * 1024;
const CONNECTION_READ_TIMEOUT: Duration = Duration::from_secs(5);

pub struct CallbackServer {
    listener: TcpListener,
    port: u16,
}

impl CallbackServer {
    pub async fn bind() -> Result<Self> {
        let listener = TcpListener::bind(("127.0.0.1", 0))
            .await
            .context("binding loopback callback listener")?;
        let port = listener.local_addr()?.port();
        Ok(Self { listener, port })
    }

    pub fn redirect_uri(&self) -> String {
        format!("http://127.0.0.1:{}{CALLBACK_PATH}", self.port)
    }

    pub async fn wait_for_code(self, expected_state: &str, timeout: Duration) -> Result<String> {
        tokio::time::timeout(timeout, self.serve(expected_state))
            .await
            .map_err(|_| anyhow!("timed out waiting for the browser login to complete"))?
    }

    async fn serve(&self, expected_state: &str) -> Result<String> {
        loop {
            let (mut stream, _) = self.listener.accept().await.context("accepting callback")?;
            let Ok(Some(target)) = read_request_target(&mut stream).await else {
                respond(&mut stream, 400, "Bad request").await;
                continue;
            };
            let Ok(url) = Url::parse(&format!("http://127.0.0.1{target}")) else {
                respond(&mut stream, 400, "Bad request").await;
                continue;
            };
            if url.path() != CALLBACK_PATH {
                respond(&mut stream, 404, "Not found").await;
                continue;
            }
            match interpret(&url, expected_state) {
                Ok(code) => {
                    respond(
                        &mut stream,
                        200,
                        "Login complete. You can close this tab and return to the terminal.",
                    )
                    .await;
                    return Ok(code);
                }
                Err(e) => {
                    respond(&mut stream, 400, "Login failed. Return to the terminal.").await;
                    return Err(e);
                }
            }
        }
    }
}

fn interpret(url: &Url, expected_state: &str) -> Result<String> {
    let mut state = None;
    let mut code = None;
    let mut error = None;
    for (k, v) in url.query_pairs() {
        match k.as_ref() {
            "state" => state = Some(v.into_owned()),
            "code" => code = Some(v.into_owned()),
            "error" => error = Some(v.into_owned()),
            _ => {}
        }
    }
    if state.as_deref() != Some(expected_state) {
        return Err(anyhow!(
            "callback state does not match this login attempt; aborting"
        ));
    }
    if let Some(e) = error {
        return Err(anyhow!("login was not completed: {e}"));
    }
    code.filter(|c| !c.is_empty())
        .ok_or_else(|| anyhow!("callback did not carry an authorization code"))
}

async fn read_request_target(stream: &mut TcpStream) -> Result<Option<String>> {
    let mut buf = Vec::new();
    let mut chunk = [0u8; 1024];
    loop {
        if buf.windows(2).any(|w| w == b"\r\n") {
            break;
        }
        if buf.len() > MAX_REQUEST_BYTES {
            return Ok(None);
        }
        let n = tokio::time::timeout(CONNECTION_READ_TIMEOUT, stream.read(&mut chunk))
            .await
            .context("reading callback request")??;
        if n == 0 {
            return Ok(None);
        }
        buf.extend_from_slice(&chunk[..n]);
    }
    let line_end = buf.windows(2).position(|w| w == b"\r\n").unwrap_or(0);
    let line = String::from_utf8_lossy(&buf[..line_end]);
    let mut parts = line.split(' ');
    match (parts.next(), parts.next()) {
        (Some("GET"), Some(target)) if target.starts_with('/') => Ok(Some(target.to_owned())),
        _ => Ok(None),
    }
}

async fn respond(stream: &mut TcpStream, status: u16, message: &str) {
    let reason = match status {
        200 => "OK",
        404 => "Not Found",
        _ => "Bad Request",
    };
    let body =
        format!("<!doctype html><meta charset=utf-8><title>FerrVault</title><p>{message}</p>");
    let head = format!(
        "HTTP/1.1 {status} {reason}\r\nContent-Type: text/html; charset=utf-8\r\nContent-Length: {}\r\nCache-Control: no-store\r\nConnection: close\r\n\r\n",
        body.len()
    );
    let _ = stream.write_all(head.as_bytes()).await;
    let _ = stream.write_all(body.as_bytes()).await;
    let _ = stream.shutdown().await;
}

#[cfg(test)]
mod tests {
    use super::*;

    async fn get(url: String) -> u16 {
        reqwest::get(url).await.unwrap().status().as_u16()
    }

    #[tokio::test]
    async fn returns_code_when_state_matches() {
        let server = CallbackServer::bind().await.unwrap();
        let redirect = server.redirect_uri();
        let waiter = tokio::spawn(server.wait_for_code("good", Duration::from_secs(5)));
        assert_eq!(get(format!("{redirect}?code=abc&state=good")).await, 200);
        assert_eq!(waiter.await.unwrap().unwrap(), "abc");
    }

    #[tokio::test]
    async fn aborts_on_state_mismatch_without_yielding_the_code() {
        let server = CallbackServer::bind().await.unwrap();
        let redirect = server.redirect_uri();
        let waiter = tokio::spawn(server.wait_for_code("good", Duration::from_secs(5)));
        assert_eq!(get(format!("{redirect}?code=stolen&state=evil")).await, 400);
        let err = waiter.await.unwrap().unwrap_err().to_string();
        assert!(err.contains("state"), "{err}");
        assert!(!err.contains("stolen"));
    }

    #[tokio::test]
    async fn missing_state_is_a_mismatch() {
        let server = CallbackServer::bind().await.unwrap();
        let redirect = server.redirect_uri();
        let waiter = tokio::spawn(server.wait_for_code("good", Duration::from_secs(5)));
        get(format!("{redirect}?code=abc")).await;
        assert!(waiter.await.unwrap().is_err());
    }

    #[tokio::test]
    async fn surfaces_idp_error_after_state_check() {
        let server = CallbackServer::bind().await.unwrap();
        let redirect = server.redirect_uri();
        let waiter = tokio::spawn(server.wait_for_code("good", Duration::from_secs(5)));
        get(format!("{redirect}?error=access_denied&state=good")).await;
        let err = waiter.await.unwrap().unwrap_err().to_string();
        assert!(err.contains("access_denied"), "{err}");
    }

    #[tokio::test]
    async fn unrelated_paths_do_not_end_the_wait() {
        let server = CallbackServer::bind().await.unwrap();
        let redirect = server.redirect_uri();
        let base = redirect.trim_end_matches("/callback").to_owned();
        let waiter = tokio::spawn(server.wait_for_code("good", Duration::from_secs(5)));
        assert_eq!(get(format!("{base}/favicon.ico")).await, 404);
        get(format!("{redirect}?code=abc&state=good")).await;
        assert_eq!(waiter.await.unwrap().unwrap(), "abc");
    }

    #[tokio::test]
    async fn times_out_when_nobody_calls_back() {
        let server = CallbackServer::bind().await.unwrap();
        let err = server
            .wait_for_code("good", Duration::from_millis(50))
            .await
            .unwrap_err()
            .to_string();
        assert!(err.contains("timed out"));
    }
}
