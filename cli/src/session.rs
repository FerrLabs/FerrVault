use std::sync::{Arc, Condvar, Mutex as StdMutex};

use anyhow::{Context, Result, anyhow};
use chrono::{DateTime, Duration, Utc};
use serde::{Deserialize, Serialize};
use tokio::sync::Mutex;
use zeroize::Zeroizing;

use crate::lock::{ProcessLock, default_lock_path};
use crate::oauth::idp::{IdpClient, IdpError, TokenGrant};
use crate::storage::CredentialStore;

const REFRESH_SKEW_SECONDS: i64 = 60;

#[derive(Clone, Serialize, Deserialize)]
pub struct Session {
    pub idp_url: String,
    pub access_token: Zeroizing<String>,
    pub refresh_token: Zeroizing<String>,
    pub expires_at: DateTime<Utc>,
    pub account: Option<String>,
    pub org: Option<String>,
    pub vault: Option<String>,
    pub environment: Option<String>,
}

impl Session {
    pub fn from_grant(idp_url: &str, grant: &TokenGrant, now: DateTime<Utc>) -> Self {
        Self {
            idp_url: idp_url.to_owned(),
            access_token: Zeroizing::new(grant.token.clone()),
            refresh_token: Zeroizing::new(grant.refresh_token.clone()),
            expires_at: now + Duration::seconds(grant.expires_in),
            account: grant
                .user
                .as_ref()
                .and_then(|u| u.email.clone().or_else(|| u.name.clone())),
            org: None,
            vault: None,
            environment: None,
        }
    }

    fn rotate(&mut self, grant: &TokenGrant, now: DateTime<Utc>) {
        self.access_token = Zeroizing::new(grant.token.clone());
        self.refresh_token = Zeroizing::new(grant.refresh_token.clone());
        self.expires_at = now + Duration::seconds(grant.expires_in);
    }

    fn needs_refresh(&self, now: DateTime<Utc>) -> bool {
        self.expires_at - Duration::seconds(REFRESH_SKEW_SECONDS) <= now
    }
}

#[derive(Serialize)]
pub struct SessionSummary {
    pub account: Option<String>,
    pub org: Option<String>,
    pub vault: Option<String>,
    pub environment: Option<String>,
    pub expires_at: DateTime<Utc>,
}

pub trait SessionStore: Send + Sync {
    fn load(&self) -> Result<Option<Session>>;
    fn save(&self, session: &Session) -> Result<()>;
    fn lock(&self) -> Result<Box<dyn Send>>;
}

pub struct KeyringSessionStore;

impl SessionStore for KeyringSessionStore {
    fn load(&self) -> Result<Option<Session>> {
        CredentialStore::load_session()
    }

    fn lock(&self) -> Result<Box<dyn Send>> {
        Ok(Box::new(ProcessLock::acquire(&default_lock_path()?)?))
    }

    fn save(&self, session: &Session) -> Result<()> {
        CredentialStore::save_session(session)
    }
}

#[derive(Default)]
pub struct MemorySessionStore {
    saved: StdMutex<Option<Session>>,
    gate: Arc<Gate>,
}

#[derive(Default)]
struct Gate {
    held: StdMutex<bool>,
    released: Condvar,
}

struct GateGuard(Arc<Gate>);

impl Drop for GateGuard {
    fn drop(&mut self) {
        if let Ok(mut held) = self.0.held.lock() {
            *held = false;
        }
        self.0.released.notify_one();
    }
}

impl MemorySessionStore {
    #[cfg(test)]
    pub fn saved(&self) -> Option<Session> {
        self.saved.lock().ok().and_then(|g| g.clone())
    }
}

impl SessionStore for MemorySessionStore {
    fn load(&self) -> Result<Option<Session>> {
        Ok(self
            .saved
            .lock()
            .map_err(|_| anyhow!("session store poisoned"))?
            .clone())
    }

    fn save(&self, session: &Session) -> Result<()> {
        *self
            .saved
            .lock()
            .map_err(|_| anyhow!("session store poisoned"))? = Some(session.clone());
        Ok(())
    }

    fn lock(&self) -> Result<Box<dyn Send>> {
        let mut held = self
            .gate
            .held
            .lock()
            .map_err(|_| anyhow!("session gate poisoned"))?;
        while *held {
            held = self
                .gate
                .released
                .wait(held)
                .map_err(|_| anyhow!("session gate poisoned"))?;
        }
        *held = true;
        Ok(Box::new(GateGuard(self.gate.clone())))
    }
}

pub struct Scope {
    pub vault: String,
    pub environment: String,
}

pub struct Credential {
    pub token: Zeroizing<String>,
    pub org: Option<String>,
}

pub struct SessionAuth {
    state: Mutex<Session>,
    idp: IdpClient,
    store: Arc<dyn SessionStore>,
}

impl SessionAuth {
    pub fn new(session: Session, idp: IdpClient, store: Arc<dyn SessionStore>) -> Self {
        Self {
            state: Mutex::new(session),
            idp,
            store,
        }
    }

    pub async fn credential(&self) -> Result<Credential> {
        let mut session = self.state.lock().await;
        if session.needs_refresh(Utc::now()) {
            self.refresh(&mut session, |s| s.needs_refresh(Utc::now()))
                .await?;
        }
        Ok(Credential {
            token: session.access_token.clone(),
            org: session.org.clone(),
        })
    }

    pub async fn refresh_after_rejection(&self, rejected_token: &str) -> Result<()> {
        let mut session = self.state.lock().await;
        if session.access_token.as_str() != rejected_token {
            return Ok(());
        }
        self.refresh(&mut session, |s| s.access_token.as_str() == rejected_token)
            .await
    }

    pub async fn select_org(&self, org: &str) {
        let mut session = self.state.lock().await;
        session.org = Some(org.to_owned());
        session.vault = None;
        session.environment = None;
    }

    pub async fn select_vault(&self, vault: &str) {
        let mut session = self.state.lock().await;
        session.vault = Some(vault.to_owned());
        session.environment = None;
    }

    pub async fn select_environment(&self, environment: &str) {
        self.state.lock().await.environment = Some(environment.to_owned());
    }

    pub async fn vault(&self) -> Option<String> {
        self.state.lock().await.vault.clone()
    }

    pub async fn scope(&self) -> Result<Scope> {
        let s = self.state.lock().await;
        match (&s.vault, &s.environment) {
            (Some(vault), Some(environment)) => Ok(Scope {
                vault: vault.clone(),
                environment: environment.clone(),
            }),
            _ => Err(anyhow!(
                "no vault and environment selected; run `ferrvault login --vault <slug> --env <slug>`"
            )),
        }
    }

    pub async fn snapshot(&self) -> Session {
        self.state.lock().await.clone()
    }

    pub async fn summary(&self) -> SessionSummary {
        let s = self.state.lock().await;
        SessionSummary {
            account: s.account.clone(),
            org: s.org.clone(),
            vault: s.vault.clone(),
            environment: s.environment.clone(),
            expires_at: s.expires_at,
        }
    }

    pub async fn sign_out(&self) -> Result<()> {
        let credential = self.credential().await?;
        self.idp.logout(&credential.token).await?;
        Ok(())
    }

    async fn refresh(
        &self,
        session: &mut Session,
        still_stale: impl Fn(&Session) -> bool,
    ) -> Result<()> {
        let store = self.store.clone();
        let _guard = tokio::task::spawn_blocking(move || store.lock())
            .await
            .context("waiting for the session lock")??;

        if let Some(stored) = self.store.load()?
            && stored.refresh_token.as_str() != session.refresh_token.as_str()
        {
            *session = stored;
        }
        if !still_stale(session) {
            return Ok(());
        }

        let grant = self
            .idp
            .refresh(&session.refresh_token)
            .await
            .map_err(|e| match e {
                IdpError::Rejected { .. } => {
                    anyhow!("session expired or revoked; run `ferrvault login` again")
                }
                other => anyhow::Error::new(other).context("refreshing session"),
            })?;
        session.rotate(&grant, Utc::now());
        self.store
            .save(session)
            .context("persisting the rotated session")
    }
}

#[cfg(test)]
mod tests {
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
    fn session_round_trips_through_json() {
        let s = session(100);
        let back: Session = serde_json::from_str(&serde_json::to_string(&s).unwrap()).unwrap();
        assert_eq!(back.refresh_token.as_str(), "old-refresh");
        assert_eq!(back.expires_at, s.expires_at);
    }
}
