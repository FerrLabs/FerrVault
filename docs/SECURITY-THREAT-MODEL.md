# FerrVault CLI — threat model

Scope: the `ferrvault` consumer CLI shipped from this repository, plus the
GitHub Action wrapper. Server-side concerns are tracked in
[`FerrLabs/FerrVault-Cloud`](https://github.com/FerrLabs/FerrVault-Cloud).

## Assets

| Asset | Where it lives | Sensitivity |
| --- | --- | --- |
| Service-account token (`fvsat_...`) | OS keyring; process env vars; HTTP headers in flight | **Critical** — direct vault read access bound to one (vault, role) |
| Decrypted secret values | CLI process memory; child process env | **Critical** — what the SAT exists to fetch |
| `FERRVAULT_URL` | OS keyring or env | Medium — disclosing tenant URL helps targeted phishing |
| `FERRVAULT_PIN_SHA256` | Env or flag | Low — public information; integrity-only |

## Trust boundaries

```
                ┌─────────────┐
                │  developer  │
                │  / runner   │
                └──────┬──────┘
                       │ (a) launches
                       ▼
              ┌────────────────┐
   keyring ◀──┤  ferrvault CLI ├──► child process (env-injected)
              └────────┬───────┘
                       │ (b) HTTPS bearer
                       ▼
              ┌────────────────┐
              │ FerrVault API  │
              └────────────────┘
```

Boundary (a): the launching shell or GitHub Actions runner. We assume the
caller is trusted to invoke the CLI but **not** to read its memory after
fork. Other processes on the host are out of our trust boundary — a host
compromise compromises the SAT, full stop.

Boundary (b): the network between the CLI and the FerrVault API.

## In scope

### T1 — token theft at rest

| Threat | Mitigation | Status |
| --- | --- | --- |
| Token written to a plain file under `~/.config` or similar | Token stored in OS keyring only (Windows DPAPI, macOS Keychain, Secret Service on Linux). No fallback to disk. | ✅ |
| Token written to shell history via `--token` flag | `--token` is supported but discouraged; CLI prompts on stdin when missing. When stdin is a tty, `login` uses `rpassword` for real input masking. README + `--help` flag it. | ✅ |
| Token logged by accident at `info` level | `tracing_subscriber` default filter is `warn`; the token is held in `Zeroizing<String>` and never formatted by `Debug` impls. | ✅ |
| Token surviving in a swapped memory page after process exit | `zeroize::Zeroizing<String>` clears the heap allocation on drop. Stack copies in `reqwest` internals are out of our control. | ⚠ partial |

### T2 — token theft in flight

| Threat | Mitigation | Status |
| --- | --- | --- |
| Plain HTTP | CLI refuses any URL whose scheme is not `https://`. | ✅ |
| TLS downgrade / cipher downgrade | rustls 0.23, TLS 1.2+ only, no `tls.disable_*` switches exposed. | ✅ |
| Compromised CA in the system trust store | `FERRVAULT_PIN_SHA256` enforces a SHA-256 pin on any cert in the server chain (leaf, intermediate, or root). Mismatch = handshake aborts before the bearer is sent. | ✅ |
| Misconfigured private CA needed | `--ca-cert path.pem` extends the trust store; pin still applies if set. | ✅ |
| Stolen client cert (mTLS) | `--client-cert` + `--client-key` supported; key file must be 0600. Treat client keys with the same care as tokens. | ✅ |
| Forgotten `--insecure-skip-verify` in a script | Flag refuses to take effect unless `FERRVAULT_INSECURE_I_KNOW_WHAT_I_AM_DOING=yes` is also in env. A dev who used it locally and forgot to remove it gets a hard error in CI, not a silent MITM-able session. | ✅ |
| Token leaked via verbose proxy logging | The CLI sends `Authorization: Bearer fvsat_...` in headers only, never in URLs. | ✅ |

### T3 — leak of decrypted values

| Threat | Mitigation | Status |
| --- | --- | --- |
| Secret value printed to logs during exec | `exec` writes secrets only into the child's environment block (`Command::env`). They never touch the CLI's stdout/stderr. | ✅ |
| Secret value persisted to disk by accident | The CLI has no `write to file` mode. `get --format dotenv` writes to stdout only — operator is responsible if they pipe to a file. | ✅ |
| Decrypted value lingering after the child exits | `Zeroizing` clears the CLI-side copy after `Command` consumes it. Child process memory is the child's responsibility. | ⚠ partial |
| Audit gap on bulk fetches | Server emits one `secret.read` event per name, with the SAT id in the metadata. The CLI does not — and cannot — bypass this. | ✅ |

### T4 — replay & abuse

| Threat | Mitigation | Status |
| --- | --- | --- |
| Stolen SAT used at line speed to dump the vault | Server-side token-bucket rate limit (60 req/min/SAT) returns 429; the CLI surfaces this clearly with the `Retry-After` value. | ✅ (server-side; see [FerrVault-Cloud#231](https://github.com/FerrLabs/FerrVault-Cloud/pull/231)) |
| Long-lived SAT outliving its purpose | Token `expires_at` is exposed via `ferrvault whoami` so ops can see the expiry and rotate. Rotation flow is server-side. | ✅ |
| SAT used against the wrong vault | Server returns `403 WRONG_VAULT` on mismatch; CLI surfaces the error verbatim. | ✅ |

### T5 — GitHub Action surface

| Threat | Mitigation | Status |
| --- | --- | --- |
| Workflow logs leak secret values | Action emits `::add-mask::<value>` for every fetched value before doing anything else. | ✅ (see `action/action.yml`) |
| Token passed via untrusted PR | Action documents that the SAT must come from a repository secret, never from a `pull_request` payload. | ✅ |
| Action downloads a compromised binary | Action pins the CLI binary by SHA-256 against the published release artifact. | ⚠ planned in follow-up |

## Out of scope

- **Host compromise.** If the machine running the CLI is owned, the SAT is owned. We do not attempt to defend against that.
- **Memory dumping during execution.** We use `Zeroize` for our own structs but do not enforce locked pages (`mlock`) or run inside a TEE.
- **DoS at the network layer.** Upstream LB / Traefik concern, not the CLI's.
- **Server-side secrets management.** Owned by `FerrVault-Cloud` (envelope encryption, KMS rotation, RBAC, audit).
- **Cross-process side channels** on shared CI runners (timing, /proc scraping, etc.). Out of scope for a userland CLI.

## Open follow-ups

1. SLSA build provenance + cosign signatures on the published binaries.
2. SHA-256 binary pin in the GitHub Action.
3. `mlock`-style page locking for the token in memory (Linux only; depends on Cargo deps maturity).
4. Optional integration with `gh secret`-style short-lived OIDC tokens (would replace long-lived SATs in CI flows).
