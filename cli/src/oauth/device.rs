use std::future::Future;
use std::time::Duration;

use anyhow::{Result, anyhow};

use super::idp::{DeviceAuthorization, DevicePoll, IdpClient, TokenGrant};

const SLOW_DOWN_STEP: Duration = Duration::from_secs(5);
const MAX_INTERVAL: Duration = Duration::from_secs(60);

pub async fn device_login<S, F>(
    idp: &IdpClient,
    announce: impl FnOnce(&DeviceAuthorization),
    sleep: S,
) -> Result<TokenGrant>
where
    S: Fn(Duration) -> F,
    F: Future<Output = ()>,
{
    let auth = idp.start_device().await?;
    announce(&auth);
    let lifetime = Duration::from_secs(auth.expires_in);
    let mut interval = Duration::from_secs(auth.interval.max(1)).min(MAX_INTERVAL);
    let mut waited = Duration::ZERO;
    loop {
        sleep(interval).await;
        waited = waited.saturating_add(interval);
        match idp.poll_device(&auth.device_code).await? {
            DevicePoll::Granted(grant) => return Ok(grant),
            DevicePoll::Pending => {}
            DevicePoll::SlowDown => {
                interval = interval.saturating_add(SLOW_DOWN_STEP).min(MAX_INTERVAL);
            }
            DevicePoll::Expired => return Err(anyhow!("the device code expired; run login again")),
            DevicePoll::Denied => return Err(anyhow!("login was denied in the browser")),
        }
        if waited >= lifetime {
            return Err(anyhow!("the device code expired; run login again"));
        }
    }
}

#[cfg(test)]
mod tests {
    use std::sync::{Arc, Mutex};

    use serde_json::{Value, json};

    use super::*;
    use crate::testing::{MockResponse, MockServer};

    fn grant() -> Value {
        json!({"token":"access-d","refresh_token":"refresh-d","expires_in":900})
    }

    fn error(code: &str) -> Value {
        json!({ "error": code })
    }

    async fn idp(
        expires_in: u64,
        interval: u64,
        polls: Vec<(u16, Value)>,
    ) -> (MockServer, IdpClient) {
        let polls = Arc::new(Mutex::new(polls.into_iter()));
        let server = MockServer::start(move |r| match r.path.as_str() {
            "/auth/device/code" => MockResponse::json(
                200,
                &json!({
                    "device_code": "dev-123",
                    "user_code": "ABCD-EFGH",
                    "verification_uri": "https://auth.example/device",
                    "verification_uri_complete": "https://auth.example/device?c=ABCD-EFGH",
                    "expires_in": expires_in,
                    "interval": interval,
                }),
            ),
            "/auth/device/token" => {
                let next = polls.lock().unwrap().next();
                let (status, body) = next.unwrap_or((400, error("authorization_pending")));
                MockResponse::json(status, &body)
            }
            other => panic!("unexpected {other}"),
        })
        .await;
        let client = IdpClient::with_http(server.url.clone(), crate::testing::http_client());
        (server, client)
    }

    async fn drive(client: &IdpClient) -> (Result<TokenGrant>, Vec<Duration>) {
        let sleeps = Arc::new(Mutex::new(Vec::new()));
        let log = sleeps.clone();
        let result = device_login(
            client,
            |_| {},
            move |d| {
                log.lock().unwrap().push(d);
                async {}
            },
        )
        .await;
        let recorded = sleeps.lock().unwrap().clone();
        (result, recorded)
    }

    fn secs(v: &[u64]) -> Vec<Duration> {
        v.iter().copied().map(Duration::from_secs).collect()
    }

    #[tokio::test]
    async fn polls_at_the_server_interval_until_granted() {
        let (server, client) = idp(
            600,
            2,
            vec![
                (400, error("authorization_pending")),
                (400, error("authorization_pending")),
                (200, grant()),
            ],
        )
        .await;
        let (result, sleeps) = drive(&client).await;
        assert_eq!(result.unwrap().token, "access-d");
        assert_eq!(sleeps, secs(&[2, 2, 2]));
        let polls = server.requests_to("/auth/device/token");
        assert_eq!(polls.len(), 3);
        let body = polls[0].json();
        assert_eq!(body["device_code"], "dev-123");
        assert_eq!(body["client_id"], "ferrvault-cli");
        assert_eq!(
            body["grant_type"],
            "urn:ietf:params:oauth:grant-type:device_code"
        );
    }

    #[tokio::test]
    async fn slow_down_adds_five_seconds_for_every_later_poll() {
        let (_server, client) = idp(
            600,
            1,
            vec![
                (400, error("slow_down")),
                (400, error("authorization_pending")),
                (400, error("slow_down")),
                (200, grant()),
            ],
        )
        .await;
        let (result, sleeps) = drive(&client).await;
        assert!(result.is_ok());
        assert_eq!(sleeps, secs(&[1, 6, 6, 11]));
    }

    #[tokio::test]
    async fn server_side_expiry_stops_polling() {
        let (server, client) = idp(600, 1, vec![(400, error("expired_token"))]).await;
        let (result, _) = drive(&client).await;
        assert!(result.err().unwrap().to_string().contains("expired"));
        assert_eq!(server.requests_to("/auth/device/token").len(), 1);
    }

    #[tokio::test]
    async fn local_deadline_stops_polling_when_the_server_stays_pending() {
        let (server, client) = idp(3, 2, vec![]).await;
        let (result, sleeps) = drive(&client).await;
        assert!(result.err().unwrap().to_string().contains("expired"));
        assert_eq!(sleeps, secs(&[2, 2]));
        assert_eq!(server.requests_to("/auth/device/token").len(), 2);
    }

    #[tokio::test]
    async fn denial_is_reported_and_stops_polling() {
        let (server, client) = idp(600, 1, vec![(400, error("access_denied"))]).await;
        let (result, _) = drive(&client).await;
        assert!(result.err().unwrap().to_string().contains("denied"));
        assert_eq!(server.requests_to("/auth/device/token").len(), 1);
    }

    #[tokio::test]
    async fn unknown_error_codes_are_surfaced_not_retried() {
        let (server, client) = idp(600, 1, vec![(400, error("invalid_client"))]).await;
        let (result, _) = drive(&client).await;
        assert!(format!("{:#}", result.err().unwrap()).contains("invalid_client"));
        assert_eq!(server.requests_to("/auth/device/token").len(), 1);
    }

    #[tokio::test]
    async fn zero_interval_is_clamped_to_one_second() {
        let (_server, client) = idp(600, 0, vec![(200, grant())]).await;
        let (_, sleeps) = drive(&client).await;
        assert_eq!(sleeps, secs(&[1]));
    }

    #[tokio::test]
    async fn an_interval_near_u64_max_with_slow_down_is_capped_not_overflowed() {
        let (_server, client) = idp(
            600,
            u64::MAX - 4,
            vec![(400, error("slow_down")), (200, grant())],
        )
        .await;
        let (result, sleeps) = drive(&client).await;
        assert!(result.is_ok());
        assert_eq!(sleeps, secs(&[60, 60]));
    }

    #[tokio::test]
    async fn a_huge_interval_and_lifetime_poll_at_the_cap_without_overflow() {
        let (_server, client) = idp(
            u64::MAX,
            1 << 63,
            vec![
                (400, error("authorization_pending")),
                (400, error("authorization_pending")),
                (200, grant()),
            ],
        )
        .await;
        let (result, sleeps) = drive(&client).await;
        assert!(result.is_ok());
        assert_eq!(sleeps, secs(&[60, 60, 60]));
    }

    #[tokio::test]
    async fn slow_down_stops_growing_at_the_cap() {
        let mut polls = vec![(400, error("slow_down")); 15];
        polls.push((200, grant()));
        let (_server, client) = idp(100_000, 55, polls).await;
        let (_, sleeps) = drive(&client).await;
        assert!(sleeps.iter().all(|d| *d <= Duration::from_secs(60)));
        assert_eq!(sleeps.last(), Some(&Duration::from_secs(60)));
    }
}
