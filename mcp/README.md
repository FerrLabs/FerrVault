# FerrVault MCP

FerrVault as Model Context Protocol servers, so an AI assistant can drive it.

[![npm @ferrvault/mcp](https://img.shields.io/npm/v/@ferrvault/mcp)](https://www.npmjs.com/package/@ferrvault/mcp)
[![npm @ferrvault/api-mcp](https://img.shields.io/npm/v/@ferrvault/api-mcp)](https://www.npmjs.com/package/@ferrvault/api-mcp)

## Quick start

Add to your MCP client configuration (Claude Code, Cursor, etc.):

```json
{
  "mcpServers": {
    "ferrvault": { "command": "npx", "args": ["-y", "@ferrvault/mcp"] },
    "ferrvault-api": { "command": "npx", "args": ["-y", "@ferrvault/api-mcp"] }
  }
}
```

The first tool call that needs auth opens a browser for an OAuth loopback flow and saves the token. To skip it (CI, headless), set `FERRLABS_API_TOKEN`. Details in the [FerrLabs MCP README](https://github.com/FerrLabs/MCP#how-auth-resolution-works).

## Tools

Tools marked **destructive** are irreversible or high-impact. MCP clients should gate them behind a confirmation.

### `@ferrvault/mcp`

| Tool                                                               | Notes                                               |
| ------------------------------------------------------------------ | --------------------------------------------------- |
| `get_vault`, `list_secrets`, `get_secret`, `get_vault_audit_log`   | read                                                |
| `list_secret_requests`, `archive_secret_request`                   | secret-request workflow                             |
| `create_vault`, `update_vault`, `delete_vault`                     | `delete_vault` is **destructive**                   |
| `create_secret`, `update_secret`, `rotate_secret`, `delete_secret` | `rotate_secret`/`delete_secret` are **destructive** |

### `@ferrvault/api-mcp`

Targets the FerrVault API (`api.ferrvault.com`, or a self-hosted instance through `FERRVAULT_API_URL`): vaults, their environments, and the secrets, secret requests and service tokens of each environment. Vaults belong to the organization the token is signed in to.

The FerrVault API accepts FerrLabs session JWTs, not `fft_*` API tokens. Over HTTP, register the client with the IdP as an `eddsa_session_jwt` OAuth client and pass its id to the MCP client (`claude mcp add --client-id`); a dynamically registered client gets an `fft_*` token, which the API refuses.

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

## Resources and prompts

| Server           | Kind     | Name                                                 | What it holds                                     |
| ---------------- | -------- | ---------------------------------------------------- | ------------------------------------------------- |
| `@ferrvault/mcp` | Resource | `ferrvault://org/{org}/project/{project}/vault/{id}` | vault name, description, secret count, timestamps |

A vault is addressed directly by URI: listing every vault would mean walking every org and every project on each request. **Vault resources carry metadata only.** Secret values stay behind `get_secret`, which needs `reveal=true` and is audit-logged server-side. No resource ever returns one.

## Configuration

| Variable                | Description                                                                                                                                                                                    | Default                     |
| ----------------------- | ---------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- | --------------------------- |
| `FERRVAULT_API_URL`     | Base URL for the FerrVault API (`@ferrvault/api-mcp`). Its host must be allowed (see `FERRLABS_MCP_ALLOWED_API_HOSTS`). For an internal CA, point `NODE_EXTRA_CA_CERTS` at the CA certificate. | `https://api.ferrvault.com` |
| `FERRVAULT_API_VERSION` | Contract version sent as `x-ferrvault-api-version` by `@ferrvault/api-mcp`.                                                                                                                    | `2026-08-04`                |

Shared variables (token, OAuth, host allowlist, HTTP transport, output cap) are read by [`@ferrlabs/mcp-core`](https://github.com/FerrLabs/MCP/tree/main/packages/mcp-core) and documented in the [FerrLabs MCP README](https://github.com/FerrLabs/MCP#configuration).

## Development

```bash
cd mcp
pnpm install
pnpm build
pnpm test
pnpm smoke
```

`pnpm smoke` boots each server over stdio and checks the handshake and the advertised tools.

## License

Mozilla Public License 2.0, see [LICENSE](../LICENSE).
