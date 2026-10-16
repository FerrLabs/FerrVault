import { z } from 'zod';
import { apiRequest, getToken, type McpServer, toToolText } from '@ferrlabs/mcp-core';

interface VaultDetails {
  id: string;
  name: string;
  description: string | null;
  secret_count: number;
  created_at: string;
  updated_at: string;
}

export async function fetchVaultDetails(
  orgSlug: string,
  projectSlug: string,
  vaultId: string,
): Promise<VaultDetails> {
  const token = await getToken();
  return apiRequest<VaultDetails>(
    `/orgs/${encodeURIComponent(orgSlug)}/projects/${encodeURIComponent(projectSlug)}/vaults/${encodeURIComponent(vaultId)}`,
    { token },
  );
}

export function registerVaultDetailsTool(server: McpServer) {
  server.tool(
    'get_vault',
    'Get detailed information about a single FerrVault vault — name, description, secret count, timestamps.',
    {
      org_slug: z.string().min(1).describe('Organization slug'),
      project_slug: z.string().min(1).describe('Project slug'),
      vault_id: z.string().min(1).describe('Vault id'),
    },
    async ({ org_slug, project_slug, vault_id }) => {
      const vault = await fetchVaultDetails(org_slug, project_slug, vault_id);
      return {
        content: [{ type: 'text' as const, text: toToolText(vault) }],
      };
    },
  );
}
