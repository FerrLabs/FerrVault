use anyhow::Result;

use crate::storage::CredentialStore;

pub fn run() -> Result<()> {
    CredentialStore::delete_token()?;
    CredentialStore::delete_url()?;
    eprintln!("ferrvault: token cleared from OS keyring.");
    Ok(())
}
