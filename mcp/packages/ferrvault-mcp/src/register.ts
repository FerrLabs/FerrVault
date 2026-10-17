import type { McpServer } from '@ferrlabs/mcp-core';
import { registerVaultTools } from './tools/vaults.js';
import { registerSecretReadTools } from './tools/secrets.js';
import { registerSecretWriteTools } from './tools/secret-writes.js';
import { registerSecretRequestTools } from './tools/secret-requests.js';
import { registerSecretCopyTools } from './tools/secret-copy.js';
import { registerServiceTokenTools } from './tools/service-tokens.js';

export function register(server: McpServer): void {
  registerVaultTools(server);
  registerSecretReadTools(server);
  registerSecretWriteTools(server);
  registerSecretRequestTools(server);
  registerSecretCopyTools(server);
  registerServiceTokenTools(server);
}
