use serde_json::json;

use super::*;
use crate::testing::{MockResponse, MockServer};

fn session(expires_in_secs: i64) -> Session {
    Session {
        idp_url: "http://idp".into(),
        access_token: Zeroizing::new("old-access".into()),
        refresh_token: Zeroizing::new("old-refresh".into()),
        expires_at: Utc::now() + Duration::seconds(expires_in_secs),
        account: Some("a@b.c".into()),
        org: Some("acme".into()),
        vault: Some("prod".into()),
        environment: Some("default".into()),
    }
}

async fn idp_rotating() -> MockServer {
    MockServer::start(|r| {
        assert_eq!(r.path, "/auth/refresh");
        MockResponse::json(
            200,
            &json!({"token":"new-access","refresh_token":"new-refresh","expires_in":900}),
        )
    })
    .await
}

fn auth_for(idp: &MockServer, s: Session, store: Arc<MemorySessionStore>) -> SessionAuth {
    let client = IdpClient::with_http(idp.url.clone(), crate::testing::http_client());
    SessionAuth::new(s, client, store)
}

#[tokio::test]
async fn fresh_session_is_used_without_contacting_the_idp() {
    let idp = idp_rotating().await;
    let store = Arc::new(MemorySessionStore::default());
    let auth = auth_for(&idp, session(900), store.clone());
    let cred = auth.credential().await.unwrap();
    assert_eq!(cred.token.as_str(), "old-access");
    assert_eq!(cred.org.as_deref(), Some("acme"));
    assert!(idp.requests().is_empty());
    assert!(store.saved().is_none());
}

#[tokio::test]
async fn expired_session_refreshes_and_persists_the_rotated_pair() {
    let idp = idp_rotating().await;
    let store = Arc::new(MemorySessionStore::default());
    let auth = auth_for(&idp, session(-5), store.clone());
    let cred = auth.credential().await.unwrap();
    assert_eq!(cred.token.as_str(), "new-access");
    let sent = idp.requests();
    assert_eq!(sent.len(), 1);
    assert_eq!(sent[0].json()["refresh_token"], "old-refresh");
    assert_eq!(sent[0].json()["client_id"], "ferrvault-cli");
    let saved = store.saved().unwrap();
    assert_eq!(saved.access_token.as_str(), "new-access");
    assert_eq!(saved.refresh_token.as_str(), "new-refresh");
    assert_eq!(saved.org.as_deref(), Some("acme"));
    assert_eq!(saved.vault.as_deref(), Some("prod"));
}

#[tokio::test]
async fn token_inside_the_skew_window_is_refreshed_early() {
    let idp = idp_rotating().await;
    let auth = auth_for(&idp, session(30), Arc::new(MemorySessionStore::default()));
    assert_eq!(
        auth.credential().await.unwrap().token.as_str(),
        "new-access"
    );
}

#[tokio::test]
async fn rejection_of_a_stale_token_does_not_rotate_twice() {
    let idp = idp_rotating().await;
    let auth = auth_for(&idp, session(-5), Arc::new(MemorySessionStore::default()));
    auth.credential().await.unwrap();
    auth.refresh_after_rejection("old-access").await.unwrap();
    assert_eq!(idp.requests().len(), 1);
}

#[tokio::test]
async fn revoked_refresh_token_asks_for_a_new_login() {
    let idp =
        MockServer::start(|_| MockResponse::json(400, &json!({"error":"invalid_grant"}))).await;
    let store = Arc::new(MemorySessionStore::default());
    let auth = auth_for(&idp, session(-5), store.clone());
    let err = auth.credential().await.err().unwrap().to_string();
    assert!(err.contains("ferrvault login"), "{err}");
    assert!(store.saved().is_none());
}

#[tokio::test]
async fn idp_outage_is_not_reported_as_an_expired_session() {
    let idp = MockServer::start(|_| MockResponse::json(503, &json!({}))).await;
    let auth = auth_for(&idp, session(-5), Arc::new(MemorySessionStore::default()));
    let err = format!("{:#}", auth.credential().await.err().unwrap());
    assert!(err.contains("unavailable"), "{err}");
    assert!(!err.contains("expired or revoked"));
}

async fn single_use_idp() -> MockServer {
    let used = Arc::new(StdMutex::new(Vec::<String>::new()));
    MockServer::start(move |r| {
        let presented = r.json()["refresh_token"].as_str().unwrap().to_owned();
        let mut used = used.lock().unwrap();
        if used.contains(&presented) {
            return MockResponse::json(400, &json!({"error":"invalid_grant"}));
        }
        used.push(presented);
        MockResponse::json(
            200,
            &json!({"token":"new-access","refresh_token":"new-refresh","expires_in":3600}),
        )
    })
    .await
}

#[tokio::test(flavor = "multi_thread", worker_threads = 2)]
async fn two_processes_refreshing_the_same_session_rotate_it_once() {
    let idp = single_use_idp().await;
    let store = Arc::new(MemorySessionStore::default());
    let first = auth_for(&idp, session(-5), store.clone());
    let second = auth_for(&idp, session(-5), store.clone());
    let (a, b) = tokio::join!(first.credential(), second.credential());
    assert_eq!(a.unwrap().token.as_str(), "new-access");
    assert_eq!(b.unwrap().token.as_str(), "new-access");
    assert_eq!(idp.requests().len(), 1);
    assert_eq!(store.saved().unwrap().refresh_token.as_str(), "new-refresh");
}

#[tokio::test]
async fn a_401_after_another_process_rotated_adopts_the_stored_pair() {
    let idp = single_use_idp().await;
    let store = Arc::new(MemorySessionStore::default());
    let mut rotated = session(3600);
    rotated.access_token = Zeroizing::new("new-access".into());
    rotated.refresh_token = Zeroizing::new("new-refresh".into());
    store.save(&rotated).unwrap();
    let auth = auth_for(&idp, session(3600), store);
    auth.refresh_after_rejection("old-access").await.unwrap();
    assert_eq!(
        auth.credential().await.unwrap().token.as_str(),
        "new-access"
    );
    assert!(idp.requests().is_empty());
}

#[tokio::test]
async fn a_stored_pair_that_is_itself_expired_is_refreshed_with_its_own_token() {
    let idp = single_use_idp().await;
    let store = Arc::new(MemorySessionStore::default());
    let mut stored = session(-5);
    stored.refresh_token = Zeroizing::new("stored-refresh".into());
    store.save(&stored).unwrap();
    let auth = auth_for(&idp, session(-5), store);
    auth.credential().await.unwrap();
    assert_eq!(idp.requests()[0].json()["refresh_token"], "stored-refresh");
}

#[tokio::test]
async fn sign_out_sends_the_access_token_as_bearer_and_never_the_refresh_token() {
    let idp = MockServer::start(|r| {
        assert_eq!(r.path, "/auth/logout");
        MockResponse {
            status: 204,
            body: String::new(),
        }
    })
    .await;
    let auth = auth_for(&idp, session(900), Arc::new(MemorySessionStore::default()));
    auth.sign_out().await.unwrap();
    let sent = idp.requests();
    assert_eq!(sent.len(), 1);
    assert_eq!(sent[0].header("authorization"), Some("Bearer old-access"));
    assert!(!sent[0].body.contains("old-refresh"));
}

#[tokio::test]
async fn sign_out_with_an_expired_access_token_refreshes_first() {
    let idp = MockServer::start(|r| {
        if r.path == "/auth/refresh" {
            MockResponse::json(
                200,
                &json!({"token":"new-access","refresh_token":"new-refresh","expires_in":900}),
            )
        } else {
            MockResponse {
                status: 204,
                body: String::new(),
            }
        }
    })
    .await;
    let auth = auth_for(&idp, session(-5), Arc::new(MemorySessionStore::default()));
    auth.sign_out().await.unwrap();
    let logout = idp.requests_to("/auth/logout");
    assert_eq!(logout[0].header("authorization"), Some("Bearer new-access"));
}

#[tokio::test]
async fn sign_out_failure_is_reported() {
    let idp =
        MockServer::start(|_| MockResponse::json(401, &json!({"error":"unauthorized"}))).await;
    let auth = auth_for(&idp, session(900), Arc::new(MemorySessionStore::default()));
    assert!(auth.sign_out().await.is_err());
}

#[test]
fn metadata_round_trips_and_stays_far_below_a_windows_credential_blob() {
    let mut s = session(100);
    s.org = Some("0198f1c2-7a3b-7c11-9d2e-5b6a4c3d2e1f".into());
    s.account = Some("a.very.long.address@subdomain.example-company.com".into());
    s.vault = Some("v".repeat(63));
    s.environment = Some("e".repeat(63));
    let json = serde_json::to_string(&s.meta()).unwrap();
    assert!(json.chars().count() < 640, "{}", json.len());
    let back = Session::from_parts(
        serde_json::from_str(&json).unwrap(),
        s.access_token.clone(),
        s.refresh_token.clone(),
    );
    assert_eq!(back.expires_at, s.expires_at);
    assert_eq!(back.vault, s.vault);
    assert_eq!(back.refresh_token.as_str(), "old-refresh");
}

fn grant_with(expires_in: i64) -> TokenGrant {
    serde_json::from_value(json!({
        "token": "a", "refresh_token": "r", "expires_in": expires_in
    }))
    .unwrap()
}

#[test]
fn expires_in_is_validated_without_panicking() {
    let now = Utc::now();
    for bad in [
        -1,
        i64::MIN,
        i64::MAX,
        10_000_000_000_000_000,
        -8_639_870_297_731_200,
    ] {
        assert!(
            matches!(
                Session::from_grant("https://idp", &grant_with(bad), now),
                Err(IdpError::Malformed(_))
            ),
            "{bad}"
        );
    }
}

#[test]
fn zero_and_ordinary_lifetimes_are_accepted() {
    let now = Utc::now();
    let zero = Session::from_grant("https://idp", &grant_with(0), now).unwrap();
    assert_eq!(zero.expires_at, now);
    assert!(zero.needs_refresh(now));
    let hour = Session::from_grant("https://idp", &grant_with(3600), now).unwrap();
    assert_eq!(hour.expires_at, now + Duration::seconds(3600));
}

#[tokio::test]
async fn a_bad_expires_in_on_refresh_still_persists_the_rotated_pair() {
    let idp = MockServer::start(|_| {
        MockResponse::json(
            200,
            &json!({"token":"new-access","refresh_token":"new-refresh","expires_in": i64::MIN}),
        )
    })
    .await;
    let store = Arc::new(MemorySessionStore::default());
    let auth = auth_for(&idp, session(-5), store.clone());
    assert!(auth.credential().await.is_err());
    let saved = store.saved().unwrap();
    assert_eq!(saved.refresh_token.as_str(), "new-refresh");
    assert_eq!(saved.access_token.as_str(), "new-access");
    assert!(saved.needs_refresh(Utc::now()));
}
