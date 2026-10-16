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

function vaultBase(org_slug: string, project_slug: string, vault_id: string): string {
  return `/orgs/${encodeURIComponent(org_slug)}/projects/${encodeURIComponent(project_slug)}/vaults/${encodeURIComponent(vault_id)}`;
}

export function registerSecretMutationTools(server: McpServer) {
  server.tool(
    'create_secret',
    'Create a new secret inside a vault. The value is encrypted server-side under the org KEK (envelope encryption) — it never lands in clear in the DB.',
    {
      org_slug: z.string().min(1).describe('Organization slug'),
      project_slug: z.string().min(1).describe('Project slug'),
      vault_id: z.string().min(1).describe('Vault id'),
      name: z
        .string()
        .min(1)
        .max(200)
        .regex(/^[A-Z][A-Z0-9_]*$/, 'name must be SCREAMING_SNAKE_CASE')
        .describe('Secret name (e.g. STRIPE_API_KEY)'),
      value: z.string().min(1).describe('Plaintext value — encrypted at rest by the server.'),
      description: z.string().max(500).optional().describe('Optional human-readable note.'),
    },
    async ({ org_slug, project_slug, vault_id, name, value, description }) => {
      const token = await getToken();
      const secret = await apiRequest<SecretSummary>(
        `${vaultBase(org_slug, project_slug, vault_id)}/secrets`,
        {
          token,
          method: 'POST',
          body: { name, value, description: description ?? null },
        },
      );
      return {
        content: [{ type: 'text' as const, text: toToolText(secret) }],
      };
    },
  );

  server.tool(
    'update_secret',
    'Update a secret. Pass `value` to rotate it in place (preserves name + id); pass `description` to retitle it. Either or both may be omitted; nothing is touched unless you pass it.',
    {
      org_slug: z.string().min(1).describe('Organization slug'),
      project_slug: z.string().min(1).describe('Project slug'),
      vault_id: z.string().min(1).describe('Vault id'),
      secret_id: z.string().min(1).describe('Secret id'),
      value: z.string().min(1).optional().describe('New plaintext value.'),
      description: z.string().max(500).nullable().optional(),
    },
    async ({ org_slug, project_slug, vault_id, secret_id, ...patch }) => {
      const token = await getToken();
      const body: Record<string, unknown> = {};
      for (const [k, v] of Object.entries(patch)) {
        if (v !== undefined) body[k] = v;
      }
      const secret = await apiRequest<SecretSummary>(
        `${vaultBase(org_slug, project_slug, vault_id)}/secrets/${encodeURIComponent(secret_id)}`,
        { token, method: 'PATCH', body },
      );
      return {
        content: [{ type: 'text' as const, text: toToolText(secret) }],
      };
    },
  );

  server.tool(
    'delete_secret',
    'Permanently delete a secret. Irreversible at the API level — recovery only via the soft-delete window on the FerrVault dashboard.',
    {
      org_slug: z.string().min(1).describe('Organization slug'),
      project_slug: z.string().min(1).describe('Project slug'),
      vault_id: z.string().min(1).describe('Vault id'),
      secret_id: z.string().min(1).describe('Secret id'),
    },
    async ({ org_slug, project_slug, vault_id, secret_id }) => {
      const token = await getToken();
      await apiRequest<void>(
        `${vaultBase(org_slug, project_slug, vault_id)}/secrets/${encodeURIComponent(secret_id)}`,
        { token, method: 'DELETE' },
      );
      return {
        content: [{ type: 'text' as const, text: `Secret ${secret_id} deleted.` }],
      };
    },
  );

  server.tool(
    'rotate_secret',
    'Rotate a secret in-place — fans the new value through any FerrVault K8s operator deployments that mount it via SecretRef, so consumers pick up the new value on next sync without an app restart.',
    {
      org_slug: z.string().min(1).describe('Organization slug'),
      project_slug: z.string().min(1).describe('Project slug'),
      vault_id: z.string().min(1).describe('Vault id'),
      secret_id: z.string().min(1).describe('Secret id'),
      value: z.string().min(1).describe('New plaintext value (will replace the current).'),
    },
    async ({ org_slug, project_slug, vault_id, secret_id, value }) => {
      const token = await getToken();
      const secret = await apiRequest<SecretSummary>(
        `${vaultBase(org_slug, project_slug, vault_id)}/secrets/${encodeURIComponent(secret_id)}/rotate`,
        { token, method: 'POST', body: { value } },
      );
      return {
        content: [{ type: 'text' as const, text: toToolText(secret) }],
      };
    },
  );

  server.tool(
    'archive_secret_request',
    'Archive a "missing secret" request — marks it handled and removes it from the active list. Use after provisioning the secret (or deciding it should not be provisioned).',
    {
      org_slug: z.string().min(1).describe('Organization slug'),
      project_slug: z.string().min(1).describe('Project slug'),
      vault_id: z.string().min(1).describe('Vault id'),
      request_id: z.string().min(1).describe('Secret-request id'),
    },
    async ({ org_slug, project_slug, vault_id, request_id }) => {
      const token = await getToken();
      await apiRequest<void>(
        `${vaultBase(org_slug, project_slug, vault_id)}/secret-requests/${encodeURIComponent(request_id)}/archive`,
        { token, method: 'POST' },
      );
      return {
        content: [{ type: 'text' as const, text: `Secret request ${request_id} archived.` }],
      };
    },
  );
}
