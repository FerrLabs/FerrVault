import { z } from 'zod';
import { apiRequest, getToken, type McpServer, toToolText } from '@ferrlabs/mcp-core';

interface AuditEntry {
  id: string;
  actor_id: string;
  action: string;
  target_id: string | null;
  details: Record<string, unknown> | null;
  created_at: string;
}

interface SecretRequest {
  id: string;
  vault_id: string;
  requested_name: string;
  requester_id: string;
  status: string;
  created_at: string;
  archived_at: string | null;
}

export function registerVaultAuditTools(server: McpServer) {
  server.tool(
    'get_vault_audit_log',
    'Recent audit-log entries for a FerrVault vault — who read/wrote/rotated what, when.',
    {
      org_slug: z.string().min(1).describe('Organization slug'),
      project_slug: z.string().min(1).describe('Project slug'),
      vault_id: z.string().min(1).describe('Vault id'),
      limit: z
        .number()
        .int()
        .min(1)
        .max(200)
        .optional()
        .describe('Max entries to return (default 50).'),
    },
    async ({ org_slug, project_slug, vault_id, limit }) => {
      const token = await getToken();
      const qs = limit !== undefined ? `?limit=${limit}` : '';
      const entries = await apiRequest<AuditEntry[]>(
        `/orgs/${encodeURIComponent(org_slug)}/projects/${encodeURIComponent(project_slug)}/vaults/${encodeURIComponent(vault_id)}/audit-log${qs}`,
        { token },
      );
      return {
        content: [
          {
            type: 'text' as const,
            text: toToolText(entries, { narrowWith: 'Pass a smaller limit.' }),
          },
        ],
      };
    },
  );

  server.tool(
    'list_secret_requests',
    'List pending "missing secret" requests filed by operators/SDKs when a name lookup misses. Useful to spot secrets that need to be provisioned.',
    {
      org_slug: z.string().min(1).describe('Organization slug'),
      project_slug: z.string().min(1).describe('Project slug'),
      vault_id: z.string().min(1).describe('Vault id'),
    },
    async ({ org_slug, project_slug, vault_id }) => {
      const token = await getToken();
      const requests = await apiRequest<SecretRequest[]>(
        `/orgs/${encodeURIComponent(org_slug)}/projects/${encodeURIComponent(project_slug)}/vaults/${encodeURIComponent(vault_id)}/secret-requests`,
        { token },
      );
      return {
        content: [{ type: 'text' as const, text: toToolText(requests) }],
      };
    },
  );
}
