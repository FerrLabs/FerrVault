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

#[derive(Serialize, Deserialize)]
pub struct SessionMeta {
    pub idp_url: String,
    pub expires_at: DateTime<Utc>,
    pub account: Option<String>,
    pub org: Option<String>,
    pub vault: Option<String>,
    pub environment: Option<String>,
}

#[derive(Clone)]
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
    pub fn meta(&self) -> SessionMeta {
        SessionMeta {
            idp_url: self.idp_url.clone(),
            expires_at: self.expires_at,
            account: self.account.clone(),
            org: self.org.clone(),
            vault: self.vault.clone(),
            environment: self.environment.clone(),
        }
    }

    pub fn from_parts(
        meta: SessionMeta,
        access: Zeroizing<String>,
        refresh: Zeroizing<String>,
    ) -> Self {
        Self {
            idp_url: meta.idp_url,
            access_token: access,
            refresh_token: refresh,
            expires_at: meta.expires_at,
            account: meta.account,
            org: meta.org,
            vault: meta.vault,
            environment: meta.environment,
        }
    }

    pub fn from_grant(
        idp_url: &str,
        grant: &TokenGrant,
        now: DateTime<Utc>,
    ) -> Result<Self, IdpError> {
        Ok(Self {
            idp_url: idp_url.to_owned(),
            access_token: Zeroizing::new(grant.token.clone()),
            refresh_token: Zeroizing::new(grant.refresh_token.clone()),
            expires_at: grant.expires_at(now)?,
            account: grant
                .user
                .as_ref()
                .and_then(|u| u.email.clone().or_else(|| u.name.clone())),
            org: None,
            vault: None,
            environment: None,
        })
    }

    fn rotate(&mut self, grant: &TokenGrant, now: DateTime<Utc>) -> Result<(), IdpError> {
        self.access_token = Zeroizing::new(grant.token.clone());
        self.refresh_token = Zeroizing::new(grant.refresh_token.clone());
        match grant.expires_at(now) {
            Ok(at) => {
                self.expires_at = at;
                Ok(())
            }
            Err(e) => {
                self.expires_at = now;
                Err(e)
            }
        }
    }

    fn needs_refresh(&self, now: DateTime<Utc>) -> bool {
        self.expires_at
            .checked_sub_signed(Duration::seconds(REFRESH_SKEW_SECONDS))
            .is_none_or(|refresh_at| refresh_at <= now)
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
    fn requires_stored_session(&self) -> bool;
}

pub struct KeyringSessionStore;

impl SessionStore for KeyringSessionStore {
    fn requires_stored_session(&self) -> bool {
        true
    }

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
    persistent: bool,
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
    pub fn persistent() -> Self {
        Self {
            persistent: true,
            ..Self::default()
        }
    }

    #[cfg(test)]
    pub fn saved(&self) -> Option<Session> {
        self.saved.lock().ok().and_then(|g| g.clone())
    }
}

impl SessionStore for MemorySessionStore {
    fn requires_stored_session(&self) -> bool {
        self.persistent
    }

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

        match self.store.load()? {
            Some(stored) if stored.refresh_token.as_str() != session.refresh_token.as_str() => {
                *session = stored;
            }
            None if self.store.requires_stored_session() => {
                return Err(anyhow!("logged out; run `ferrvault login` again"));
            }
            _ => {}
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
        let rotated = session.rotate(&grant, Utc::now());
        self.store
            .save(session)
            .context("persisting the rotated session")?;
        rotated.context("refreshing session")
    }
}

#[cfg(test)]
mod tests;
