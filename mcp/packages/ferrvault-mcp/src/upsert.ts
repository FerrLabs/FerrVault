import { ApiRequestError } from '@ferrlabs/mcp-core';
import { type SecretTarget, secretPath, secretsPath, vaultRequest } from './api.js';
import type { SecretMetadata } from './secret.js';

export interface Upserted {
  created: boolean;
  secret: SecretMetadata;
}

function isAlreadyThere(err: unknown): boolean {
  return err instanceof ApiRequestError && err.status === 409 && err.code === 'SECRET_EXISTS';
}

export async function upsertSecret(
  token: string,
  target: SecretTarget,
  name: string,
  value: string,
): Promise<Upserted> {
  try {
    const secret = await vaultRequest<SecretMetadata>(secretsPath(target), {
      token,
      method: 'POST',
      body: { name, value },
    });
    return { created: true, secret };
  } catch (err) {
    if (!isAlreadyThere(err)) throw err;
  }
  const secret = await vaultRequest<SecretMetadata>(secretPath(target, name), {
    token,
    method: 'PUT',
    body: { value },
  });
  return { created: false, secret };
}

export function outcome<Existing extends 'rotated' | 'versioned'>(
  { created }: Upserted,
  existing: Existing,
): 'created' | Existing {
  return created ? 'created' : existing;
}
