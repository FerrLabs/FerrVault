import { ApiRequestError, toToolText } from '@ferrlabs/mcp-core';

export interface ToolResult {
  [key: string]: unknown;
  content: [{ type: 'text'; text: string }];
  isError?: true;
}

export type Redact = (secret: string) => void;

export function textResult(value: unknown): ToolResult {
  return { content: [{ type: 'text', text: toToolText(value) }] };
}

function redact(message: string, secrets: readonly string[]): string {
  return secrets.reduce((text, secret) => text.split(secret).join('[redacted]'), message);
}

export function describeError(err: unknown, secrets: readonly string[] = []): string {
  const message = redact(err instanceof Error ? err.message : String(err), secrets);
  if (err instanceof ApiRequestError) {
    const code = err.code ? ` ${err.code}` : '';
    return `FerrVault API error (HTTP ${err.status}${code}): ${message}`;
  }
  return message;
}

function errorResult(err: unknown, secrets: readonly string[]): ToolResult {
  return {
    isError: true,
    content: [{ type: 'text', text: describeError(err, secrets) }],
  };
}

export async function guarded(
  run: (redactLater: Redact) => Promise<ToolResult>,
  secret?: string,
): Promise<ToolResult> {
  const secrets = secret ? [secret] : [];
  try {
    return await run((later) => {
      if (later) secrets.push(later);
    });
  } catch (err) {
    return errorResult(err, secrets);
  }
}
