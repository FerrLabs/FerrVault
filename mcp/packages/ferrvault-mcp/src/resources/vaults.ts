import { ResourceTemplate, toToolText, type McpServer } from '@ferrlabs/mcp-core';
import { fetchVaultDetails } from '../tools/vault-details.js';

export function registerVaultResources(server: McpServer) {
  server.registerResource(
    'vault-metadata',
    new ResourceTemplate('ferrvault://org/{org}/project/{project}/vault/{id}', {
      list: () => ({ resources: [] }),
    }),
    {
      title: 'Vault metadata',
      description:
        'Name, description, secret count and timestamps for one vault. Never the secret values: those stay behind get_secret and its reveal gate.',
      mimeType: 'application/json',
    },
    async (uri, { org, project, id }) => {
      const vault = await fetchVaultDetails(String(org), String(project), String(id));
      return {
        contents: [{ uri: uri.href, mimeType: 'application/json', text: toToolText(vault) }],
      };
    },
  );
}
