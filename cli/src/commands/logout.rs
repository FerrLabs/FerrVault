use std::sync::Arc;

use anyhow::Result;

use super::connection::{idp_client, tls_args};
use crate::api::TlsArgs;
use crate::cli::Cli;
use crate::session::{KeyringSessionStore, Session, SessionAuth};
use crate::storage::CredentialStore;

pub async fn run(cli: &Cli) -> Result<()> {
    end_stored_session(&tls_args(cli)?).await;
    CredentialStore::delete_token()?;
    CredentialStore::delete_session()?;
    CredentialStore::delete_url()?;
    eprintln!("ferrvault: credentials cleared from OS keyring.");
    Ok(())
}

pub async fn end_stored_session(tls: &TlsArgs<'_>) {
    if let Ok(Some(session)) = CredentialStore::load_session()
        && let Err(e) = sign_out(session, tls).await
    {
        eprintln!("ferrvault: could not end the previous session remotely ({e:#}).");
    }
}

async fn sign_out(session: Session, tls: &TlsArgs<'_>) -> Result<()> {
    let idp = idp_client(&session, tls)?;
    SessionAuth::new(session, idp, Arc::new(KeyringSessionStore))
        .sign_out()
        .await
}
