import { z } from 'zod';
import { apiRequest, getToken, type McpServer, toToolText } from '@ferrlabs/mcp-core';

interface VaultSummary {
  id: string;
  name: string;
  description: string | null;
  secret_count: number;
  created_at: string;
  updated_at: string;
}

function vaultBase(org_slug: string, project_slug: string, vault_id: string): string {
  return `/orgs/${encodeURIComponent(org_slug)}/projects/${encodeURIComponent(project_slug)}/vaults/${encodeURIComponent(vault_id)}`;
}

export function registerVaultMutationTools(server: McpServer) {
  server.tool(
    'create_vault',
    'Create a new vault inside a project. Empty by default — populate via the FerrVault app or via secret-request workflows.',
    {
      org_slug: z.string().min(1).describe('Organization slug'),
      project_slug: z.string().min(1).describe('Project slug'),
      name: z.string().min(1).max(100).describe('Human-readable vault name'),
      description: z.string().max(500).optional().describe('Optional description for teammates'),
    },
    async ({ org_slug, project_slug, name, description }) => {
      const token = await getToken();
      const vault = await apiRequest<VaultSummary>(
        `/orgs/${encodeURIComponent(org_slug)}/projects/${encodeURIComponent(project_slug)}/vaults`,
        { token, method: 'POST', body: { name, description: description ?? null } },
      );
      return {
        content: [{ type: 'text' as const, text: toToolText(vault) }],
      };
    },
  );

  server.tool(
    'update_vault',
    'Patch a vault — rename it or change its description. Only the fields you pass are touched.',
    {
      org_slug: z.string().min(1).describe('Organization slug'),
      project_slug: z.string().min(1).describe('Project slug'),
      vault_id: z.string().min(1).describe('Vault id'),
      name: z.string().min(1).max(100).optional(),
      description: z.string().max(500).nullable().optional(),
    },
    async ({ org_slug, project_slug, vault_id, ...patch }) => {
      const token = await getToken();
      const body: Record<string, unknown> = {};
      for (const [k, v] of Object.entries(patch)) {
        if (v !== undefined) body[k] = v;
      }
      const vault = await apiRequest<VaultSummary>(vaultBase(org_slug, project_slug, vault_id), {
        token,
        method: 'PATCH',
        body,
      });
      return {
        content: [{ type: 'text' as const, text: toToolText(vault) }],
      };
    },
  );

  server.tool(
    'delete_vault',
    'Delete a vault and ALL its secrets. Irreversible. The server enforces a soft-delete window; check the FerrVault UI to confirm.',
    {
      org_slug: z.string().min(1).describe('Organization slug'),
      project_slug: z.string().min(1).describe('Project slug'),
      vault_id: z.string().min(1).describe('Vault id'),
    },
    async ({ org_slug, project_slug, vault_id }) => {
      const token = await getToken();
      await apiRequest<void>(vaultBase(org_slug, project_slug, vault_id), {
        token,
        method: 'DELETE',
      });
      return {
        content: [{ type: 'text' as const, text: `Vault ${vault_id} deleted.` }],
      };
    },
  );
}
