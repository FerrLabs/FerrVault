import { z } from 'zod';
import { getToken, type McpServer } from '@ferrlabs/mcp-core';
import { type SecretTarget, serviceTokenPath, serviceTokensPath, vaultRequest } from '../api.js';
import { describeError, guarded, textResult } from '../results.js';
import {
  environmentSlug,
  expiresAt,
  secretName,
  serviceTokenId,
  serviceTokenName,
  vaultRole,
  vaultSlug,
} from '../schemas.js';
import { type Upserted, upsertSecret, outcome } from '../upsert.js';

interface ServiceTokenRow {
  id: string;
  label: string;
  role: z.infer<typeof vaultRole>;
  created_by: string;
  created_at: string;
  expires_at: string | null;
  last_used_at: string | null;
}

interface CreatedServiceToken extends ServiceTokenRow {
  token: string;
}

function metadata(row: ServiceTokenRow, scope: SecretTarget) {
  return {
    id: row.id,
    name: row.label,
    vault: scope.vault,
    environment: scope.environment,
    role: row.role,
    created_by: row.created_by,
    created_at: row.created_at,
    expires_at: row.expires_at,
    last_used_at: row.last_used_at,
  };
}

async function revokeQuietly(token: string, scope: SecretTarget, id: string): Promise<boolean> {
  try {
    await vaultRequest<void>(serviceTokenPath(scope, id), { token, method: 'DELETE' });
    return true;
  } catch {
    return false;
  }
}

async function storeOrRevoke(
  token: string,
  scope: SecretTarget,
  created: CreatedServiceToken,
  into: SecretTarget & { name: string },
): Promise<Upserted> {
  try {
    return await upsertSecret(
      token,
      { vault: into.vault, environment: into.environment },
      into.name,
      created.token,
    );
  } catch (err) {
    const revoked = await revokeQuietly(token, scope, created.id);
    throw new Error(
      `storing service token ${created.id} failed (${describeError(err)}); ${
        revoked ? 'the token was revoked' : 'revoking it failed too, revoke it by id'
      }`,
    );
  }
}

const store = z
  .object({ vault: vaultSlug, environment: environmentSlug, name: secretName })
  .describe('Secret the new token value is written to, created or versioned');

export function registerServiceTokenTools(server: McpServer): void {
  server.tool(
    'list_ferrvault_service_tokens',
    'List the active service tokens (operator tokens) bound to one vault environment: id, name, role, creator, creation, expiry and last use. Never includes the token value. Requires the admin role on the vault.',
    { vault: vaultSlug, environment: environmentSlug },
    ({ vault, environment }) =>
      guarded(async () => {
        const token = await getToken();
        const scope = { vault, environment };
        const rows = await vaultRequest<ServiceTokenRow[]>(serviceTokensPath(scope), { token });
        return textResult(rows.map((row) => metadata(row, scope)));
      }),
  );

  server.tool(
    'create_ferrvault_service_token',
    'Create a service token (operator token) bound to one vault environment with a role, and write its value into a secret (created, or versioned when it exists) inside the MCP server. The token value is never returned, so it never enters the conversation. If the write fails, the new token is revoked. Requires the admin role on the vault, and write access to the store secret.',
    {
      vault: vaultSlug,
      environment: environmentSlug,
      name: serviceTokenName,
      role: vaultRole,
      expires_at: expiresAt.optional(),
      store,
    },
    ({ vault, environment, name, role, expires_at, store: into }) =>
      guarded(async (redactLater) => {
        const token = await getToken();
        const scope = { vault, environment };
        const created = await vaultRequest<CreatedServiceToken>(serviceTokensPath(scope), {
          token,
          method: 'POST',
          body: { label: name, role, expires_at },
        });
        redactLater(created.token);
        const written = await storeOrRevoke(token, scope, created, into);
        return textResult({
          token: metadata(created, scope),
          stored: {
            outcome: outcome(written, 'versioned'),
            ...into,
            version: written.secret.current_version,
          },
        });
      }),
  );

  server.tool(
    'revoke_ferrvault_service_token',
    'Revoke a service token of one vault environment by id. Workloads using it lose access immediately. Requires the admin role on the vault.',
    { vault: vaultSlug, environment: environmentSlug, id: serviceTokenId },
    ({ vault, environment, id }) =>
      guarded(async () => {
        const token = await getToken();
        await vaultRequest<void>(serviceTokenPath({ vault, environment }, id), {
          token,
          method: 'DELETE',
        });
        return textResult({ revoked: id, vault, environment });
      }),
  );
}
