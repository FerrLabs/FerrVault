use std::sync::Arc;

use anyhow::Result;

use super::connection::{idp_client, tls_args};
use crate::cli::Cli;
use crate::session::{KeyringSessionStore, Session, SessionAuth};
use crate::storage::CredentialStore;

pub async fn run(cli: &Cli) -> Result<()> {
    if let Ok(Some(session)) = CredentialStore::load_session()
        && let Err(e) = sign_out(session, cli).await
    {
        eprintln!("ferrvault: could not end the session remotely ({e:#}); clearing local copy.");
    }
    CredentialStore::delete_token()?;
    CredentialStore::delete_session()?;
    CredentialStore::delete_url()?;
    eprintln!("ferrvault: credentials cleared from OS keyring.");
    Ok(())
}

async fn sign_out(session: Session, cli: &Cli) -> Result<()> {
    let tls = tls_args(cli)?;
    let idp = idp_client(&session, &tls)?;
    SessionAuth::new(session, idp, Arc::new(KeyringSessionStore))
        .sign_out()
        .await
}
