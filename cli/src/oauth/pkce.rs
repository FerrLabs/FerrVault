use anyhow::{Result, anyhow};
use base64::{Engine as _, engine::general_purpose::URL_SAFE_NO_PAD};
use sha2::{Digest, Sha256};
use zeroize::Zeroizing;

const VERIFIER_BYTES: usize = 32;
const STATE_BYTES: usize = 16;

pub struct Pkce {
    verifier: Zeroizing<String>,
    challenge: String,
}

impl Pkce {
    pub fn generate() -> Result<Self> {
        Ok(Self::from_verifier(random_token(VERIFIER_BYTES)?))
    }

    pub fn from_verifier(verifier: String) -> Self {
        let challenge = challenge_for(&verifier);
        Self {
            verifier: Zeroizing::new(verifier),
            challenge,
        }
    }

    pub fn verifier(&self) -> &str {
        &self.verifier
    }

    pub fn challenge(&self) -> &str {
        &self.challenge
    }
}

pub fn challenge_for(verifier: &str) -> String {
    URL_SAFE_NO_PAD.encode(Sha256::digest(verifier.as_bytes()))
}

pub fn random_state() -> Result<String> {
    random_token(STATE_BYTES)
}

fn random_token(len: usize) -> Result<String> {
    let mut buf = vec![0u8; len];
    getrandom::fill(&mut buf).map_err(|e| anyhow!("reading OS randomness: {e}"))?;
    Ok(URL_SAFE_NO_PAD.encode(buf))
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn challenge_matches_rfc7636_vector() {
        assert_eq!(
            challenge_for("dBjftJeZ4CVP-mB92K27uhbUJU1p1r_wW1gFWFOEjXk"),
            "E9Melhoa2OwvFrEMTJguCHaoeK1t8URWbuGJSstw-cM"
        );
    }

    #[test]
    fn generated_verifier_is_unreserved_charset_and_in_rfc_length_range() {
        let p = Pkce::generate().unwrap();
        let v = p.verifier();
        assert!((43..=128).contains(&v.len()));
        assert!(
            v.bytes()
                .all(|b| b.is_ascii_alphanumeric() || b == b'-' || b == b'_')
        );
        assert_eq!(p.challenge(), challenge_for(v));
    }

    #[test]
    fn generated_values_are_not_reused() {
        let a = Pkce::generate().unwrap();
        let b = Pkce::generate().unwrap();
        assert_ne!(a.verifier(), b.verifier());
        assert_ne!(random_state().unwrap(), random_state().unwrap());
    }
}
