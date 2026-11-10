# FerrVault MCP

FerrVault as a Model Context Protocol server, so an AI assistant can drive it.

[![npm @ferrvault/mcp](https://img.shields.io/npm/v/@ferrvault/mcp)](https://www.npmjs.com/package/@ferrvault/mcp)

## Quick start

Add to your MCP client configuration (Claude Code, Cursor, etc.):

```json
{
  "mcpServers": {
    "ferrvault": { "command": "npx", "args": ["-y", "@ferrvault/mcp"] }
  }
}
```

See [Authentication](#authentication) for how the server gets its token.

## Tools

Tools marked **destructive** are irreversible or high-impact. MCP clients should gate them behind a confirmation.

### `@ferrvault/mcp`

Targets the FerrVault API (`api.ferrvault.com`, or a self-hosted instance through `FERRVAULT_API_URL`): vaults, their environments, and the secrets, secret requests and service tokens of each environment. Vaults belong to the organization the token is signed in to.

| Tool                                                   | Notes                                                                                                                                                                                                      |
| ------------------------------------------------------ | ---------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| `list_ferrvault_vaults`, `list_ferrvault_environments` | read                                                                                                                                                                                                       |
| `create_ferrvault_vault`                               | slug and display name, in the signed-in organization; FerrVault provisions its key, makes you admin and creates a `default` environment                                                                    |
| `create_ferrvault_environment`                         | slug and display name in an existing vault; vault admin                                                                                                                                                    |
| `list_ferrvault_secrets`                               | names, versions, tags and expiry; never values                                                                                                                                                             |
| `reveal_ferrvault_secret`                              | returns the plaintext value, audit-logged by FerrVault                                                                                                                                                     |
| `set_ferrvault_secret`                                 | creates the secret, or stores a new version when it exists                                                                                                                                                 |
| `generate_ferrvault_secret`                            | random value generated in the MCP server, stored like `set_ferrvault_secret`, never returned                                                                                                               |
| `delete_ferrvault_secret`                              | **destructive**                                                                                                                                                                                            |
| `list_ferrvault_secret_requests`                       | pending requests only: names clients asked for that have no value, with counts, timestamps and requester kind                                                                                              |
| `archive_ferrvault_secret_request`                     | by id, or by name (resolved to the pending request); stays archived even if clients keep asking                                                                                                            |
| `copy_ferrvault_secret`                                | copies a value to another vault, environment or name (name defaults to the source), stored like `set_ferrvault_secret`; read and written in the MCP server, never returned; refuses source equal to target |
| `list_ferrvault_service_tokens`                        | operator tokens of one environment: id, name, role, creator, created, expiry, last use; never the token; vault admin                                                                                       |
| `create_ferrvault_service_token`                       | name, role, optional expiry; the token is written into a target secret (created or versioned) and never returned, and revoked again if that write fails; vault admin                                       |
| `revoke_ferrvault_service_token`                       | by id; **destructive**, workloads using it lose access; vault admin                                                                                                                                        |

## Authentication

The FerrVault API accepts a FerrLabs API token (`fft_...`) or a session JWT. The server picks its token in this order:

1. The bearer token of the request, over HTTP (`FERRLABS_MCP_MODE=http`). There is no other source in that mode.
2. `FERRLABS_API_TOKEN`.
3. The token saved by an earlier OAuth login.
4. An OAuth loopback login in the browser, which saves the token. `FERRLABS_MCP_NO_OAUTH=1` turns it off and the call fails asking for `FERRLABS_API_TOKEN`.

For CI and headless use, create a token in your FerrLabs account and pass it:

```json
{
  "mcpServers": {
    "ferrvault": {
      "command": "npx",
      "args": ["-y", "@ferrvault/mcp"],
      "env": { "FERRLABS_API_TOKEN": "fft_..." }
    }
  }
}
```

What a token can do:

- It never holds more than its owner: vault grants and organization membership still apply. The vault is created in, and listed from, the token's own organization.
- Every request is checked against FerrLabs, so a revoked token stops working at once. A rejected token gives a 401. Organization tokens have no owner and are always rejected.
- The token needs the scope of each tool, and scopes do not imply each other (`*` holds them all):

| Scope           | Tools                                                                                                                                                                                                                       |
| --------------- | --------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| `secrets:read`  | `list_ferrvault_vaults`, `list_ferrvault_environments`, `list_ferrvault_secrets`, `reveal_ferrvault_secret`, `list_ferrvault_secret_requests`, and the read side of `copy_ferrvault_secret`                                 |
| `secrets:write` | `create_ferrvault_vault`, `create_ferrvault_environment`, `set_ferrvault_secret`, `generate_ferrvault_secret`, `delete_ferrvault_secret`, `archive_ferrvault_secret_request`, and the write side of `copy_ferrvault_secret` |

A tool called without its scope fails with a 403, code `TOKEN_SCOPE_MISSING` and the message `this API token needs the secrets:write scope`.

The service token tools (`list_`, `create_` and `revoke_ferrvault_service_token`) need a signed-in session: with an `fft_` token they fail with 403 `SESSION_REQUIRED`. The same goes for any other route not listed above.

## Configuration

| Variable                | Description                                                                                                                                                                                | Default                     |
| ----------------------- | ------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------ | --------------------------- |
| `FERRVAULT_API_URL`     | Base URL for the FerrVault API (`@ferrvault/mcp`). Its host must be allowed (see `FERRLABS_MCP_ALLOWED_API_HOSTS`). For an internal CA, point `NODE_EXTRA_CA_CERTS` at the CA certificate. | `https://api.ferrvault.com` |
| `FERRVAULT_API_VERSION` | Contract version sent as `x-ferrvault-api-version` by `@ferrvault/mcp`.                                                                                                                    | `2026-08-04`                |

Shared variables (OAuth, host allowlist, HTTP transport, output cap) are read by [`@ferrlabs/mcp-core`](https://github.com/FerrLabs/FerrLabs/tree/main/packages/mcp-core) and documented in the [FerrLabs MCP README](https://github.com/FerrLabs/FerrLabs/blob/main/packages/mcp/README.md#configuration).

## Development

```bash
cd mcp
pnpm install
pnpm build
pnpm test
pnpm smoke
```

`pnpm smoke` boots the server over stdio and checks the handshake and the advertised tools.

## License

Mozilla Public License 2.0, see [LICENSE](../LICENSE).
