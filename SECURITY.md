# Security Policy

## Supported Versions

| Version | Supported |
|---------|-----------|
| latest  | Yes       |

Only the latest release receives security updates.

## Reporting a Vulnerability

If you discover a security vulnerability, please report it privately via [GitHub Security Advisories](https://github.com/FerrLabs/FerrVault/security/advisories/new).

Do **not** open a public issue for security vulnerabilities.

You can expect an initial response within 48 hours. We will work with you to understand the issue and coordinate a fix before any public disclosure.

## Threat model

The `ferrvault` consumer CLI ships with a written threat model at
[`docs/SECURITY-THREAT-MODEL.md`](docs/SECURITY-THREAT-MODEL.md). It enumerates
the assets the CLI is responsible for (service-account tokens, decrypted
secret values), the trust boundaries we assume, and the mitigations we
provide against token theft, network attacks, and accidental disclosure.
Read it before contributing changes that touch the auth flow, TLS stack,
storage layer, or exec wrapper.
