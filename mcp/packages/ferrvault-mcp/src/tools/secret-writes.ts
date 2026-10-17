import { z } from 'zod';
import { getToken, type McpServer } from '@ferrlabs/mcp-core';
import type { SecretTarget } from '../api.js';
import { CHARSETS, type Charset, generateValue } from '../generate.js';
import { guarded, textResult } from '../results.js';
import { environmentSlug, secretName, vaultSlug } from '../schemas.js';
import { withoutValue } from '../secret.js';
import { type Upserted, outcome, upsertSecret } from '../upsert.js';

function summary(written: Upserted, target: SecretTarget, extra: object = {}) {
  return {
    outcome: outcome(written, 'rotated'),
    ...target,
    ...extra,
    secret: withoutValue(written.secret),
  };
}

const charsetNames = Object.keys(CHARSETS) as [Charset, ...Charset[]];

export function registerSecretWriteTools(server: McpServer): void {
  server.tool(
    'set_ferrvault_secret',
    'Create a secret, or store a new version of it when it already exists (the previous version stays restorable in FerrVault). The value you pass is part of this conversation; to avoid that, use generate_ferrvault_secret.',
    {
      vault: vaultSlug,
      environment: environmentSlug,
      name: secretName,
      value: z.string().min(1).max(65_536).describe('Plaintext value, encrypted by FerrVault'),
    },
    ({ vault, environment, name, value }) =>
      guarded(async () => {
        const token = await getToken();
        const target = { vault, environment };
        return textResult(summary(await upsertSecret(token, target, name, value), target));
      }),
  );

  server.tool(
    'generate_ferrvault_secret',
    'Generate a cryptographically random value inside the MCP server and store it as a secret, creating it or rotating it to a new version. The value is never returned, so it never enters the conversation; reveal_ferrvault_secret reads it back if the user asks.',
    {
      vault: vaultSlug,
      environment: environmentSlug,
      name: secretName,
      length: z
        .number()
        .int()
        .min(8)
        .max(4096)
        .optional()
        .describe('Number of characters (default 48)'),
      charset: z
        .enum(charsetNames)
        .optional()
        .describe(
          'alphanumeric (default), hex, base64url, or ascii (alphanumeric plus punctuation, no quotes, backslash or space)',
        ),
    },
    ({ vault, environment, name, length = 48, charset = 'alphanumeric' }) => {
      const value = generateValue(length, charset);
      return guarded(async () => {
        const token = await getToken();
        const target = { vault, environment };
        const written = await upsertSecret(token, target, name, value);
        return textResult(summary(written, target, { length, charset }));
      }, value);
    },
  );
}
