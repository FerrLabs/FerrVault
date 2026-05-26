# `FerrLabs/FerrVault/action`

GitHub Action that fetches secrets from a FerrVault tenant using the
`ferrvault` CLI and injects them into the workflow as masked
environment variables.

```yaml
- uses: FerrLabs/FerrVault/action@v1
  with:
    api-url: https://api.ferrvault.example.com
    token: ${{ secrets.FERRVAULT_TOKEN }}
    names: DATABASE_URL,STRIPE_KEY
```

## Inputs

| Name             | Required | Default  | Notes                                                                 |
| ---------------- | -------- | -------- | --------------------------------------------------------------------- |
| `api-url`        | yes      | —        | FerrVault API base URL.                                               |
| `token`          | yes      | —        | SAT (`fvsat_...`). Always pass via `${{ secrets.* }}`.                |
| `names`          | no       | `''`     | Comma- or newline-separated list. Mutually exclusive with `all`.      |
| `all`            | no       | `false`  | Fetch every secret in the vault bound to this token.                  |
| `export-env`     | no       | `true`   | Export each value as a `GITHUB_ENV` variable for subsequent steps.    |
| `output-prefix`  | no       | `''`     | Prefix prepended to each exported variable name.                      |
| `ca-cert`        | no       | `''`     | Path to an additional CA certificate (PEM). Loaded into the trust store. |
| `pin-sha256`     | no       | `''`     | SHA-256 pin against any cert in the server chain (hex or base64).     |
| `cli-version`    | no       | `latest` | `ferrvault` CLI version to install.                                   |

Every fetched value is registered with `::add-mask::` **before** anything
is written to `$GITHUB_ENV`, so values cannot leak into workflow logs.

## Outputs

| Name    | Description                                       |
| ------- | ------------------------------------------------- |
| `count` | Number of secrets fetched (0 if all are missing). |

## Threat model

See [`docs/SECURITY-THREAT-MODEL.md`](../docs/SECURITY-THREAT-MODEL.md) for
the full threat model. The action-specific risks are in section T5.
