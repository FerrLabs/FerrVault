use std::fs;
use std::path::Path;
use std::sync::Arc;
use std::time::Duration;

use anyhow::{Context, Result, anyhow};
use base64::{Engine as _, engine::general_purpose::STANDARD as B64};
use reqwest::{Client, Identity};
use rustls::DigitallySignedStruct;
use rustls::client::WebPkiServerVerifier;
use rustls::client::danger::{HandshakeSignatureValid, ServerCertVerified, ServerCertVerifier};
use rustls::crypto::ring::default_provider;
use rustls::pki_types::{CertificateDer, ServerName, UnixTime};
use rustls::{ClientConfig, RootCertStore, SignatureScheme};
use sha2::{Digest, Sha256};

const USER_AGENT: &str = concat!("ferrvault-cli/", env!("CARGO_PKG_VERSION"));

pub struct TlsOptions<'a> {
    pub ca_cert_path: Option<&'a Path>,
    pub client_cert_path: Option<&'a Path>,
    pub client_key_path: Option<&'a Path>,
    pub pin_sha256_b64: Option<&'a str>,
    pub insecure_skip_verify: bool,
}

pub fn build_client(opts: &TlsOptions<'_>) -> Result<Client> {
    if opts.insecure_skip_verify {
        eprintln!(
            "ferrvault: WARNING --insecure-skip-verify is set; server TLS certificate is NOT validated."
        );
    }

    rustls::crypto::CryptoProvider::install_default(default_provider()).ok();
    let provider = Arc::new(default_provider());

    let mut roots = RootCertStore::empty();
    roots
        .roots
        .extend(webpki_roots::TLS_SERVER_ROOTS.iter().cloned());
    if let Some(p) = opts.ca_cert_path {
        let certs = load_pem_certs(p)?;
        let (_added, ignored) = roots.add_parsable_certificates(certs);
        if ignored > 0 {
            eprintln!(
                "ferrvault: {ignored} certificate(s) in {} could not be parsed and were ignored.",
                p.display()
            );
        }
    }

    let pin = match opts.pin_sha256_b64 {
        Some(s) => Some(parse_pin(s)?),
        None => None,
    };

    let verifier: Arc<dyn ServerCertVerifier> = if opts.insecure_skip_verify {
        Arc::new(SkipVerify::new(provider.clone()))
    } else {
        let inner = WebPkiServerVerifier::builder_with_provider(Arc::new(roots), provider.clone())
            .build()
            .map_err(|e| anyhow!("webpki verifier build: {e}"))?;
        match pin {
            Some(expected) => Arc::new(PinningVerifier {
                inner,
                expected_spki_sha256: expected,
            }),
            None => inner,
        }
    };

    let tls = ClientConfig::builder_with_provider(provider)
        .with_safe_default_protocol_versions()
        .map_err(|e| anyhow!("rustls protocol versions: {e}"))?
        .dangerous()
        .with_custom_certificate_verifier(verifier)
        .with_no_client_auth();

    let mut builder = Client::builder().use_preconfigured_tls(tls);
    if let (Some(cert), Some(key)) = (opts.client_cert_path, opts.client_key_path) {
        let identity = load_identity(cert, key)?;
        builder = builder.identity(identity);
    }
    builder = apply_common(builder);
    Ok(builder.build()?)
}

fn apply_common(b: reqwest::ClientBuilder) -> reqwest::ClientBuilder {
    b.user_agent(USER_AGENT)
        .https_only(true)
        .timeout(Duration::from_secs(30))
        .connect_timeout(Duration::from_secs(10))
        .pool_idle_timeout(Some(Duration::from_secs(30)))
}

fn load_pem_certs(path: &Path) -> Result<Vec<CertificateDer<'static>>> {
    let bytes = fs::read(path).with_context(|| format!("reading {}", path.display()))?;
    let mut cursor = std::io::Cursor::new(bytes);
    let certs: Result<Vec<_>, _> = rustls_pemfile::certs(&mut cursor).collect();
    certs.with_context(|| format!("parsing certs in {}", path.display()))
}

fn load_identity(cert: &Path, key: &Path) -> Result<Identity> {
    let mut pem = fs::read(cert).with_context(|| format!("reading cert {}", cert.display()))?;
    let key_bytes = fs::read(key).with_context(|| format!("reading key {}", key.display()))?;
    pem.push(b'\n');
    pem.extend(key_bytes);
    Identity::from_pem(&pem).context("constructing mTLS identity from PEM")
}

fn parse_pin(s: &str) -> Result<[u8; 32]> {
    let trimmed = s
        .trim()
        .trim_start_matches("sha256-")
        .trim_start_matches("sha256/");
    let raw = if trimmed.len() == 64 {
        hex_decode(trimmed)?
    } else {
        B64.decode(trimmed)
            .context("pin is neither valid hex nor base64")?
    };
    raw.try_into()
        .map_err(|_| anyhow!("pin must decode to 32 bytes"))
}

fn hex_decode(s: &str) -> Result<Vec<u8>> {
    if s.len() % 2 != 0 {
        return Err(anyhow!("odd hex length"));
    }
    (0..s.len())
        .step_by(2)
        .map(|i| u8::from_str_radix(&s[i..i + 2], 16).context("invalid hex"))
        .collect()
}

#[derive(Debug)]
struct PinningVerifier {
    inner: Arc<WebPkiServerVerifier>,
    expected_spki_sha256: [u8; 32],
}

impl ServerCertVerifier for PinningVerifier {
    fn verify_server_cert(
        &self,
        end_entity: &CertificateDer<'_>,
        intermediates: &[CertificateDer<'_>],
        server_name: &ServerName<'_>,
        ocsp: &[u8],
        now: UnixTime,
    ) -> Result<ServerCertVerified, rustls::Error> {
        let verified =
            self.inner
                .verify_server_cert(end_entity, intermediates, server_name, ocsp, now)?;
        let chain = std::iter::once(end_entity).chain(intermediates.iter());
        for cert in chain {
            let h: [u8; 32] = Sha256::digest(cert.as_ref()).into();
            if h == self.expected_spki_sha256 {
                return Ok(verified);
            }
        }
        Err(rustls::Error::General(
            "FERRVAULT_PIN_SHA256 does not match any certificate in the server chain".into(),
        ))
    }

    fn verify_tls12_signature(
        &self,
        message: &[u8],
        cert: &CertificateDer<'_>,
        dss: &DigitallySignedStruct,
    ) -> Result<HandshakeSignatureValid, rustls::Error> {
        self.inner.verify_tls12_signature(message, cert, dss)
    }

    fn verify_tls13_signature(
        &self,
        message: &[u8],
        cert: &CertificateDer<'_>,
        dss: &DigitallySignedStruct,
    ) -> Result<HandshakeSignatureValid, rustls::Error> {
        self.inner.verify_tls13_signature(message, cert, dss)
    }

    fn supported_verify_schemes(&self) -> Vec<SignatureScheme> {
        self.inner.supported_verify_schemes()
    }
}

#[derive(Debug)]
struct SkipVerify {
    provider: Arc<rustls::crypto::CryptoProvider>,
}

impl SkipVerify {
    fn new(provider: Arc<rustls::crypto::CryptoProvider>) -> Self {
        Self { provider }
    }
}

impl ServerCertVerifier for SkipVerify {
    fn verify_server_cert(
        &self,
        _end_entity: &CertificateDer<'_>,
        _intermediates: &[CertificateDer<'_>],
        _server_name: &ServerName<'_>,
        _ocsp: &[u8],
        _now: UnixTime,
    ) -> Result<ServerCertVerified, rustls::Error> {
        Ok(ServerCertVerified::assertion())
    }

    fn verify_tls12_signature(
        &self,
        _message: &[u8],
        _cert: &CertificateDer<'_>,
        _dss: &DigitallySignedStruct,
    ) -> Result<HandshakeSignatureValid, rustls::Error> {
        Ok(HandshakeSignatureValid::assertion())
    }

    fn verify_tls13_signature(
        &self,
        _message: &[u8],
        _cert: &CertificateDer<'_>,
        _dss: &DigitallySignedStruct,
    ) -> Result<HandshakeSignatureValid, rustls::Error> {
        Ok(HandshakeSignatureValid::assertion())
    }

    fn supported_verify_schemes(&self) -> Vec<SignatureScheme> {
        self.provider
            .signature_verification_algorithms
            .supported_schemes()
    }
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn parses_hex_pin() {
        let h = "aabbccddeeff00112233445566778899aabbccddeeff00112233445566778899";
        let p = parse_pin(h).expect("hex pin parsed");
        assert_eq!(p[0], 0xaa);
        assert_eq!(p[31], 0x99);
    }

    #[test]
    fn parses_base64_pin() {
        let raw = [0x42u8; 32];
        let b64 = B64.encode(raw);
        let p = parse_pin(&b64).expect("b64 pin parsed");
        assert_eq!(p, raw);
    }

    #[test]
    fn parses_prefixed_pin() {
        let h = "sha256-aabbccddeeff00112233445566778899aabbccddeeff00112233445566778899";
        let p = parse_pin(h).expect("prefixed pin parsed");
        assert_eq!(p[0], 0xaa);
    }

    #[test]
    fn rejects_short_pin() {
        let res = parse_pin("deadbeef");
        assert!(res.is_err());
    }
}
