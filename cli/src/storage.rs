use anyhow::{Context, Result, anyhow};
use keyring::Entry;
use zeroize::Zeroizing;

const SERVICE: &str = "ferrvault-cli";
const TOKEN_KEY: &str = "token";
const URL_KEY: &str = "api_url";

pub struct CredentialStore;

impl CredentialStore {
    pub fn save_token(token: &str) -> Result<()> {
        let entry = Entry::new(SERVICE, TOKEN_KEY)
            .context("opening OS keyring (token slot) — is one installed?")?;
        entry
            .set_password(token)
            .context("writing token to OS keyring")?;
        Ok(())
    }

    pub fn load_token() -> Result<Option<Zeroizing<String>>> {
        let entry = Entry::new(SERVICE, TOKEN_KEY).context("opening OS keyring (token slot)")?;
        match entry.get_password() {
            Ok(p) => Ok(Some(Zeroizing::new(p))),
            Err(keyring::Error::NoEntry) => Ok(None),
            Err(e) => Err(anyhow!("reading token from OS keyring: {e}")),
        }
    }

    pub fn delete_token() -> Result<()> {
        let entry = Entry::new(SERVICE, TOKEN_KEY).context("opening OS keyring (token slot)")?;
        match entry.delete_credential() {
            Ok(()) | Err(keyring::Error::NoEntry) => Ok(()),
            Err(e) => Err(anyhow!("deleting token from OS keyring: {e}")),
        }
    }

    pub fn save_url(url: &str) -> Result<()> {
        let entry = Entry::new(SERVICE, URL_KEY).context("opening OS keyring (url slot)")?;
        entry
            .set_password(url)
            .context("writing url to OS keyring")?;
        Ok(())
    }

    pub fn load_url() -> Result<Option<String>> {
        let entry = Entry::new(SERVICE, URL_KEY).context("opening OS keyring (url slot)")?;
        match entry.get_password() {
            Ok(u) => Ok(Some(u)),
            Err(keyring::Error::NoEntry) => Ok(None),
            Err(e) => Err(anyhow!("reading url from OS keyring: {e}")),
        }
    }

    pub fn delete_url() -> Result<()> {
        let entry = Entry::new(SERVICE, URL_KEY).context("opening OS keyring (url slot)")?;
        match entry.delete_credential() {
            Ok(()) | Err(keyring::Error::NoEntry) => Ok(()),
            Err(e) => Err(anyhow!("deleting url from OS keyring: {e}")),
        }
    }
}
