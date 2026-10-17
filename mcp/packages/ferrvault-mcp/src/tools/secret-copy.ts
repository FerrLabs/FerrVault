import { z } from 'zod';
import { getToken, type McpServer } from '@ferrlabs/mcp-core';
import { secretPath, vaultRequest } from '../api.js';
import { guarded, textResult } from '../results.js';
import { environmentSlug, secretName, vaultSlug } from '../schemas.js';
import type { RevealedSecret } from '../secret.js';
import { upsertSecret, outcome } from '../upsert.js';

const source = z
  .object({ vault: vaultSlug, environment: environmentSlug, name: secretName })
  .describe('Secret to copy from');

const target = z
  .object({ vault: vaultSlug, environment: environmentSlug, name: secretName.optional() })
  .describe('Where to store the copy; name defaults to the source name');

type SecretRef = z.infer<typeof source>;

function sameSecret(a: SecretRef, b: SecretRef): boolean {
  return a.vault === b.vault && a.environment === b.environment && a.name === b.name;
}

export function registerSecretCopyTools(server: McpServer): void {
  server.tool(
    'copy_ferrvault_secret',
    'Copy the current value of a secret to another vault, environment or name, creating the target or storing a new version of it. The value is read and written inside the MCP server and never returned, so it never enters the conversation. The read is audit-logged by FerrVault.',
    { source, target },
    ({ source: from, target: to }) =>
      guarded(async (redactLater) => {
        const dest = { ...to, name: to.name ?? from.name };
        if (sameSecret(from, dest)) {
          throw new Error('source and target are the same secret');
        }
        const token = await getToken();
        const revealed = await vaultRequest<RevealedSecret>(
          secretPath({ vault: from.vault, environment: from.environment }, from.name),
          { token },
        );
        redactLater(revealed.value);
        const written = await upsertSecret(
          token,
          { vault: dest.vault, environment: dest.environment },
          dest.name,
          revealed.value,
        );
        return textResult({
          outcome: outcome(written, 'versioned'),
          target: dest,
          source_version: revealed.current_version,
          target_version: written.secret.current_version,
        });
      }),
  );
}
