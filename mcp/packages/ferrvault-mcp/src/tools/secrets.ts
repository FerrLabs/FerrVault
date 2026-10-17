import { getToken, type McpServer } from '@ferrlabs/mcp-core';
import { secretPath, secretsPath, vaultRequest } from '../api.js';
import { guarded, textResult } from '../results.js';
import { environmentSlug, secretName, vaultSlug } from '../schemas.js';
import { type RevealedSecret, type SecretMetadata, withoutValue } from '../secret.js';

export function registerSecretReadTools(server: McpServer): void {
  server.tool(
    'list_ferrvault_secrets',
    'List the secrets of one vault environment: names, versions, tags and expiry. Never includes values.',
    { vault: vaultSlug, environment: environmentSlug },
    ({ vault, environment }) =>
      guarded(async () => {
        const token = await getToken();
        const secrets = await vaultRequest<(SecretMetadata & { value?: unknown })[]>(
          secretsPath({ vault, environment }),
          { token },
        );
        return textResult(secrets.map(withoutValue));
      }),
  );

  server.tool(
    'reveal_ferrvault_secret',
    'Reveal the current plaintext value of one secret. The read is audit-logged by FerrVault and the value lands in this conversation transcript: only call it when the user explicitly asks to see the value. To create or rotate a value without exposing it, use generate_ferrvault_secret.',
    { vault: vaultSlug, environment: environmentSlug, name: secretName },
    ({ vault, environment, name }) =>
      guarded(async () => {
        const token = await getToken();
        return textResult(
          await vaultRequest<RevealedSecret>(secretPath({ vault, environment }, name), { token }),
        );
      }),
  );

  server.tool(
    'delete_ferrvault_secret',
    'Delete a secret from one vault environment. Workloads reading it lose access.',
    { vault: vaultSlug, environment: environmentSlug, name: secretName },
    ({ vault, environment, name }) =>
      guarded(async () => {
        const token = await getToken();
        await vaultRequest<void>(secretPath({ vault, environment }, name), {
          token,
          method: 'DELETE',
        });
        return textResult({ deleted: name, vault, environment });
      }),
  );
}
