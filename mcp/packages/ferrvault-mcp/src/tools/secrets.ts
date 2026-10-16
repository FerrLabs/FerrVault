import { z } from 'zod';
import { apiRequest, getToken, type McpServer, toToolText } from '@ferrlabs/mcp-core';

interface SecretSummary {
  id: string;
  name: string;
  description: string | null;
  created_at: string;
  updated_at: string;
  rotated_at: string | null;
}

interface SecretRevealed extends SecretSummary {
  value: string;
}

export function registerSecretTools(server: McpServer) {
  server.tool(
    'list_secrets',
    'List secrets inside a FerrVault vault. Values are NOT included — use get_secret with reveal=true to see a specific value.',
    {
      org_slug: z.string().min(1).describe('Organization slug'),
      project_slug: z.string().min(1).describe('Project slug'),
      vault_id: z.string().min(1).describe('Vault id'),
    },
    async ({ org_slug, project_slug, vault_id }) => {
      const token = await getToken();
      const secrets = await apiRequest<SecretSummary[]>(
        `/orgs/${encodeURIComponent(org_slug)}/projects/${encodeURIComponent(project_slug)}/vaults/${encodeURIComponent(vault_id)}/secrets`,
        { token },
      );
      return {
        content: [{ type: 'text' as const, text: toToolText(secrets) }],
      };
    },
  );

  server.tool(
    'get_secret',
    'Get one secret. By default returns metadata only; pass reveal=true to also include the decrypted value (audit-logged on the server).',
    {
      org_slug: z.string().min(1).describe('Organization slug'),
      project_slug: z.string().min(1).describe('Project slug'),
      vault_id: z.string().min(1).describe('Vault id'),
      secret_id: z.string().min(1).describe('Secret id'),
      reveal: z
        .boolean()
        .optional()
        .describe('Include the decrypted value in the response (default false).'),
    },
    async ({ org_slug, project_slug, vault_id, secret_id, reveal }) => {
      const token = await getToken();
      const qs = reveal ? '?reveal=true' : '';
      const secret = await apiRequest<SecretRevealed>(
        `/orgs/${encodeURIComponent(org_slug)}/projects/${encodeURIComponent(project_slug)}/vaults/${encodeURIComponent(vault_id)}/secrets/${encodeURIComponent(secret_id)}${qs}`,
        { token },
      );
      return {
        content: [{ type: 'text' as const, text: toToolText(secret) }],
      };
    },
  );
}
