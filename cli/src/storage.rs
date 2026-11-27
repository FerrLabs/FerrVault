use std::sync::OnceLock;

use anyhow::{Context, Result, anyhow};
use keyring_core::{Entry, Error as KeyringError};
use zeroize::Zeroizing;

use crate::session::Session;

const SERVICE: &str = "ferrvault-cli";
const TOKEN_KEY: &str = "token";
const URL_KEY: &str = "api_url";
const SESSION_KEY: &str = "session";
const SESSION_ACCESS_KEY: &str = "session.access";
const SESSION_REFRESH_KEY: &str = "session.refresh";

fn open_entry(key: &str, slot: &str) -> Result<Entry> {
    static STORE: OnceLock<Result<(), String>> = OnceLock::new();
    STORE
        .get_or_init(set_native_default_store)
        .as_ref()
        .map_err(|e| anyhow!("initialising OS keyring — is one installed? ({e})"))?;
    Entry::new(SERVICE, key).with_context(|| format!("opening OS keyring ({slot} slot)"))
}

fn set_native_default_store() -> Result<(), String> {
    use std::collections::HashMap;
    let config: HashMap<&str, &str> = HashMap::new();
    #[cfg(target_os = "linux")]
    {
        let store = zbus_secret_service_keyring_store::Store::new_with_configuration(&config)
            .map_err(|e| e.to_string())?;
        keyring_core::set_default_store(store);
        Ok(())
    }
    #[cfg(target_os = "macos")]
    {
        let store = apple_native_keyring_store::keychain::Store::new_with_configuration(&config)
            .map_err(|e| e.to_string())?;
        keyring_core::set_default_store(store);
        Ok(())
    }
    #[cfg(target_os = "windows")]
    {
        let store = windows_native_keyring_store::Store::new_with_configuration(&config)
            .map_err(|e| e.to_string())?;
        keyring_core::set_default_store(store);
        Ok(())
    }
    #[cfg(not(any(target_os = "linux", target_os = "macos", target_os = "windows")))]
    {
        let _ = config;
        Err("no OS keyring backend is available on this platform".to_string())
    }
}

pub struct CredentialStore;

impl CredentialStore {
    pub fn save_token(token: &str) -> Result<()> {
        open_entry(TOKEN_KEY, "token")?
            .set_password(token)
            .context("writing token to OS keyring")?;
        Ok(())
    }

    pub fn load_token() -> Result<Option<Zeroizing<String>>> {
        match open_entry(TOKEN_KEY, "token")?.get_password() {
            Ok(p) => Ok(Some(Zeroizing::new(p))),
            Err(KeyringError::NoEntry) => Ok(None),
            Err(e) => Err(anyhow!("reading token from OS keyring: {e}")),
        }
    }

    pub fn delete_token() -> Result<()> {
        match open_entry(TOKEN_KEY, "token")?.delete_credential() {
            Ok(()) | Err(KeyringError::NoEntry) => Ok(()),
            Err(e) => Err(anyhow!("deleting token from OS keyring: {e}")),
        }
    }

    pub fn save_url(url: &str) -> Result<()> {
        open_entry(URL_KEY, "url")?
            .set_password(url)
            .context("writing url to OS keyring")?;
        Ok(())
    }

    pub fn load_url() -> Result<Option<String>> {
        match open_entry(URL_KEY, "url")?.get_password() {
            Ok(u) => Ok(Some(u)),
            Err(KeyringError::NoEntry) => Ok(None),
            Err(e) => Err(anyhow!("reading url from OS keyring: {e}")),
        }
    }

    pub fn delete_url() -> Result<()> {
        match open_entry(URL_KEY, "url")?.delete_credential() {
            Ok(()) | Err(KeyringError::NoEntry) => Ok(()),
            Err(e) => Err(anyhow!("deleting url from OS keyring: {e}")),
        }
    }

    pub fn save_session(session: &Session) -> Result<()> {
        set_secret(
            SESSION_REFRESH_KEY,
            "session refresh",
            &session.refresh_token,
        )?;
        set_secret(SESSION_ACCESS_KEY, "session access", &session.access_token)?;
        let meta = serde_json::to_string(&session.meta()).context("encoding session")?;
        set_secret(SESSION_KEY, "session", &meta)
    }

    pub fn load_session() -> Result<Option<Session>> {
        let Some(meta) = get_secret(SESSION_KEY, "session")? else {
            return Ok(None);
        };
        let incomplete = || anyhow!("stored session is incomplete; run `ferrvault login` again");
        let access = get_secret(SESSION_ACCESS_KEY, "session access")?.ok_or_else(incomplete)?;
        let refresh = get_secret(SESSION_REFRESH_KEY, "session refresh")?.ok_or_else(incomplete)?;
        let meta = serde_json::from_str(&meta)
            .context("decoding stored session; run `ferrvault login` again")?;
        Ok(Some(Session::from_parts(meta, access, refresh)))
    }

    pub fn delete_session() -> Result<()> {
        for (key, slot) in [
            (SESSION_KEY, "session"),
            (SESSION_ACCESS_KEY, "session access"),
            (SESSION_REFRESH_KEY, "session refresh"),
        ] {
            match open_entry(key, slot)?.delete_credential() {
                Ok(()) | Err(KeyringError::NoEntry) => {}
                Err(e) => return Err(anyhow!("deleting {slot} from OS keyring: {e}")),
            }
        }
        Ok(())
    }
}

fn set_secret(key: &str, slot: &str, value: &str) -> Result<()> {
    open_entry(key, slot)?
        .set_password(value)
        .with_context(|| format!("writing {slot} to OS keyring"))
}

fn get_secret(key: &str, slot: &str) -> Result<Option<Zeroizing<String>>> {
    match open_entry(key, slot)?.get_password() {
        Ok(p) => Ok(Some(Zeroizing::new(p))),
        Err(KeyringError::NoEntry) => Ok(None),
        Err(e) => Err(anyhow!("reading {slot} from OS keyring: {e}")),
    }
}
