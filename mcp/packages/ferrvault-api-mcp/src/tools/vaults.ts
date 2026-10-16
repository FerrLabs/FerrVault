import { getToken, type McpServer } from '@ferrlabs/mcp-core';
import { vaultPath, vaultRequest } from '../api.js';
import { guarded, textResult } from '../results.js';
import { displayName, newEnvironmentSlug, newVaultSlug, vaultSlug } from '../schemas.js';

export function registerVaultTools(server: McpServer): void {
  server.tool(
    'list_ferrvault_vaults',
    'List the FerrVault vaults of the organization the token is signed in to, with your role, environment slugs and secret counts.',
    {},
    () =>
      guarded(async () => {
        const token = await getToken();
        return textResult(await vaultRequest<unknown>('/vaults', { token }));
      }),
  );

  server.tool(
    'list_ferrvault_environments',
    'List the environments (e.g. dev, prod) of a FerrVault vault.',
    { vault: vaultSlug },
    ({ vault }) =>
      guarded(async () => {
        const token = await getToken();
        return textResult(
          await vaultRequest<unknown>(`${vaultPath(vault)}/environments`, { token }),
        );
      }),
  );

  server.tool(
    'create_ferrvault_vault',
    'Create a FerrVault vault in the organization the token is signed in to. FerrVault provisions its encryption key, makes you its admin and creates one environment with slug default; add more with create_ferrvault_environment.',
    { slug: newVaultSlug, name: displayName },
    ({ slug, name }) =>
      guarded(async () => {
        const token = await getToken();
        return textResult(
          await vaultRequest<unknown>('/vaults', { token, method: 'POST', body: { slug, name } }),
        );
      }),
  );

  server.tool(
    'create_ferrvault_environment',
    'Create an environment (e.g. staging) in a FerrVault vault. Requires the admin role on the vault.',
    { vault: vaultSlug, slug: newEnvironmentSlug, name: displayName },
    ({ vault, slug, name }) =>
      guarded(async () => {
        const token = await getToken();
        return textResult(
          await vaultRequest<unknown>(`${vaultPath(vault)}/environments`, {
            token,
            method: 'POST',
            body: { slug, name },
          }),
        );
      }),
  );
}
