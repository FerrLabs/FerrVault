use anyhow::Result;

use super::connection::{idp_client, tls_args};
use crate::cli::Cli;
use crate::session::Session;
use crate::storage::CredentialStore;

pub async fn run(cli: &Cli) -> Result<()> {
    if let Ok(Some(session)) = CredentialStore::load_session()
        && let Err(e) = revoke(&session, cli).await
    {
        eprintln!("ferrvault: could not revoke the session remotely ({e:#}); clearing local copy.");
    }
    CredentialStore::delete_token()?;
    CredentialStore::delete_session()?;
    CredentialStore::delete_url()?;
    eprintln!("ferrvault: credentials cleared from OS keyring.");
    Ok(())
}

async fn revoke(session: &Session, cli: &Cli) -> Result<()> {
    let tls = tls_args(cli)?;
    idp_client(session, &tls)?
        .revoke(&session.refresh_token)
        .await?;
    Ok(())
}
