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
# Interactive terminal: opens your browser (authorization code + PKCE on a
# 127.0.0.1 loopback port) and stores the session in the OS keyring.
ferrvault login --url https://api.ferrvault.example.com

# Host without a browser: prints a short code to confirm on another device.
ferrvault login --url https://api.ferrvault.example.com --device

# Internal deployment with a private CA and its own identity provider.
ferrvault --ca-cert ~/ca.pem login --url https://api.ferrvault.example --device --idp-url https://auth.example

# Several organizations, vaults or environments? Pick them up front (or answer
# the prompt). --org takes the organization id.
ferrvault login --url https://api.ferrvault.example.com --org <org-id> --vault payments --env default

# Service-account token (CI, scripts): unchanged.
ferrvault login --url https://api.ferrvault.example.com --with-token
printf '%s' "$SAT" | ferrvault login --url https://api.ferrvault.example.com
```

`login` picks the method like this: `--token` / `FERRVAULT_TOKEN` or a piped
stdin means a `fvsat_` token; `--device` means the device flow; an interactive
terminal with no token opens the browser. `--with-token` forces the masked
token prompt on a terminal.

Logging in again, or switching to a SAT, ends the previous OAuth session at the
identity provider first (a failure only warns).

A `fvsat_` token is validated against `/v1/operator/me` before anything is
stored. An OAuth login stores an access token and a refresh token; the access
token is refreshed transparently when it expires or the API answers 401, and
the rotated pair is written back to the keyring. Refreshes are serialized across CLI processes with a lock file under your state directory (it holds no data), because the identity provider ends the session if a rotated refresh token is reused. If the refresh token is
revoked or expired, run `ferrvault login` again. `FERRVAULT_TOKEN` and
`--token` always win over a stored session.

An OAuth session calls the same user routes as the FerrVault app, with the
session as bearer token and the organization in `x-ferrvault-org`, so your
vault roles apply and audit entries name you. It is scoped to the vault and
environment chosen at login (re-run `login` to change them). With a session,
`get --names` is all-or-nothing: one missing name fails the whole call.

The identity provider defaults to `https://auth.ferrlabs.com`
(`--idp-url` / `FERRVAULT_IDP_URL` to override).

The endpoints are resolved through the provider's discovery document
(`/.well-known/oauth-authorization-server/api`, then
`/.well-known/oauth-authorization-server`, then
`/api/.well-known/oauth-authorization-server`), so `--idp-url` works with or
without `/api` and with a trailing slash. If the URL answers with a web page
instead of JSON (the static front end), `login` fails and names the URL it
called and the `<idp>/api` URL to try. The refresh token is sent to the issuer
the discovery document advertises, and an https provider cannot downgrade its
endpoints to http.

When no browser can be opened (no `DISPLAY`, `WAYLAND_DISPLAY` or `BROWSER` on
Linux, or the platform opener exits non-zero), `login` switches to the device
flow by itself and prints the code to confirm on another device.

`login` checks the keyring first, by writing and deleting a throwaway entry,
and exits before any code is shown if the keyring is not usable. `--ca-cert` and
`--insecure-skip-verify` apply to it as well; `--client-cert` and
`--pin-sha256` apply only to the FerrVault API.

## Commands

| Command                       | What it does                                                |
| ----------------------------- | ----------------------------------------------------------- |
| `ferrvault login`             | Log in (browser, `--device`, or SAT) and store the credentials in the OS keyring. |
| `ferrvault logout`            | End the OAuth session at the identity provider where possible, then forget url + credentials. |
| `ferrvault whoami [--json]`   | Show the bound vault, role, label, and expiry (SAT) or user, org and role, vault, environment and access token expiry (OAuth). |
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
| `--insecure-skip-verify`      | Skip TLS validation. Requires `FERRVAULT_INSECURE_I_KNOW_WHAT_I_AM_DOING=yes` in env (belt-and-suspenders so a forgotten dev flag can't leak a SAT). **Never use in production.** |

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

The credentials (a SAT, or an OAuth session: the access token, the refresh token and a small
metadata entry with the selected org, vault and environment, kept as separate
keyring entries to stay under per-entry size limits) are stored in the OS-native credential
store:

- Windows → DPAPI / Credential Manager
- macOS → Keychain
- Linux → libsecret (Secret Service) — install `gnome-keyring` or
  `keepassxc` if you don't already have one.

There is no fallback to a plain file on disk. If no keyring is
available, `login` fails loudly. CI environments should pass a SAT
via `FERRVAULT_TOKEN` instead of using `login`.

On a headless Linux host the Secret Service often has no unlocked default
collection (`SS error: result not returned from SS API`). Unlock one, with the
passphrase in `$KEYRING_PASSWORD`:

```bash
systemctl --user stop gnome-keyring-daemon.service gnome-keyring-daemon.socket
printf '%s' "$KEYRING_PASSWORD" | gnome-keyring-daemon --daemonize --login
gnome-keyring-daemon --start --components=secrets
```

or skip the keyring and use a service token with `FERRVAULT_TOKEN`.

An OAuth session is a user-wide credential (every org and vault the account
can reach), unlike a SAT, which is bound to one vault. The CLI never prints
it; `logout` ends the session at the identity provider, which revokes the refresh token.

## Threat model

See [`docs/SECURITY-THREAT-MODEL.md`](../docs/SECURITY-THREAT-MODEL.md) at
the repo root.
