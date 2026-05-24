# `ferrvault` CLI

Consumer CLI for [FerrVault](https://ferrvault.com). Fetch secrets from
your FerrVault tenant and inject them into a process without ever writing
them to disk.

## Install

Binary releases for Linux, macOS, and Windows ship at
[releases](https://github.com/FerrLabs/FerrVault/releases). From source:

```bash
cargo install --git https://github.com/FerrLabs/FerrVault ferrvault-cli
```

## First login

```bash
# Stores the URL and token in the OS keyring. No file on disk.
ferrvault login --url https://api.ferrvault.example.com
# token will be read from stdin if not passed via --token / FERRVAULT_TOKEN
```

The login flow validates the token against `/v1/operator/me` before
storing anything, so a typo fails loudly instead of silently.

## Commands

| Command                       | What it does                                                |
| ----------------------------- | ----------------------------------------------------------- |
| `ferrvault login`             | Store url + token in OS keyring.                            |
| `ferrvault logout`            | Forget url + token.                                         |
| `ferrvault whoami [--json]`   | Show the bound vault, role, label, and expiry.              |
| `ferrvault list [--json]`     | List secret names + current version (no values).            |
| `ferrvault get NAME`          | Print a single secret value to stdout.                      |
| `ferrvault get --all`         | Print every secret (with `--format env/dotenv/json`).       |
| `ferrvault get --names A,B,C` | Fetch a subset.                                             |
| `ferrvault exec -- CMD ...`   | Run `CMD` with every secret injected as env var; never writes to disk. |
| `ferrvault set NAME VALUE`    | Create a secret. `--stdin` reads from stdin, `--from-file <path>` reads from a file, `--update` rotates an existing secret instead of erroring. Requires SAT role ≥ Writer. |
| `ferrvault delete NAME`       | Soft-delete a secret (versions are kept). Asks for typed confirmation unless `--yes` is passed. Requires SAT role ≥ Writer. |

## Security flags

| Flag                          | Effect                                                                 |
| ----------------------------- | ---------------------------------------------------------------------- |
| `--ca-cert PATH`              | Add a CA cert (PEM) to the trust store. For private deployments.       |
| `--client-cert` / `--client-key` | mTLS — client certificate + key (PEM).                              |
| `--pin-sha256 HEX|BASE64`     | Pin the server certificate by SHA-256 of any cert in the chain.        |
| `--insecure-skip-verify`      | Skip TLS validation. Prints a warning. **Never use in production.**    |

Every flag has a matching `FERRVAULT_*` environment variable. See
`ferrvault --help` for the full list.

## Examples

Run a job with secrets injected:

```bash
ferrvault exec -- ./run-migrations.sh
```

Generate a `.env` file from secrets in the vault:

```bash
ferrvault get --all --format dotenv > .env.staging
```

Use only a subset for one specific command:

```bash
ferrvault exec --names DATABASE_URL,STRIPE_KEY -- npm run start
```

## Storage

The token is stored in the OS-native credential store:

- Windows → DPAPI / Credential Manager
- macOS → Keychain
- Linux → libsecret (Secret Service) — install `gnome-keyring` or
  `keepassxc` if you don't already have one.

There is no fallback to a plain file on disk. If no keyring is
available, `login` fails loudly. CI environments should pass the token
via `FERRVAULT_TOKEN` instead of using `login`.

## Threat model

See [`docs/SECURITY-THREAT-MODEL.md`](../docs/SECURITY-THREAT-MODEL.md) at
the repo root.
