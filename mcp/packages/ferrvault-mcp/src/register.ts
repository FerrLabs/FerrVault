import type { McpServer } from '@ferrlabs/mcp-core';
import { registerVaultDetailsTool } from './tools/vault-details.js';
import { registerSecretTools } from './tools/secrets.js';
import { registerVaultAuditTools } from './tools/audit.js';
import { registerVaultMutationTools } from './tools/vaults.js';
import { registerSecretMutationTools } from './tools/secret-mutations.js';
import { registerVaultResources } from './resources/vaults.js';

export function register(server: McpServer): void {
  registerVaultDetailsTool(server);
  registerSecretTools(server);
  registerVaultAuditTools(server);
  registerVaultMutationTools(server);
  registerSecretMutationTools(server);
  registerVaultResources(server);
}
