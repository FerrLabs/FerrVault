import { apiRequest } from '@ferrlabs/mcp-core';

const API_VERSION_HEADER = 'x-ferrvault-api-version';

const FERRVAULT_API_URL = (process.env.FERRVAULT_API_URL ?? 'https://api.ferrvault.com').replace(
  /\/+$/,
  '',
);

const FERRVAULT_API_VERSION = process.env.FERRVAULT_API_VERSION ?? '2026-08-04';

interface VaultRequestOptions {
  method?: 'GET' | 'POST' | 'PUT' | 'DELETE';
  body?: unknown;
  token: string;
}

export function vaultRequest<T>(path: string, options: VaultRequestOptions): Promise<T> {
  return apiRequest<T>(path, {
    ...options,
    baseUrl: FERRVAULT_API_URL,
    headers: { [API_VERSION_HEADER]: FERRVAULT_API_VERSION },
  });
}

export interface SecretTarget {
  vault: string;
  environment: string;
}

export function vaultPath(vault: string): string {
  return `/vaults/${encodeURIComponent(vault)}`;
}

export function secretsPath({ vault, environment }: SecretTarget): string {
  return `${vaultPath(vault)}/environments/${encodeURIComponent(environment)}/secrets`;
}

export function secretPath(target: SecretTarget, name: string): string {
  return `${secretsPath(target)}/${encodeURIComponent(name)}`;
}

export function secretRequestsPath({ vault, environment }: SecretTarget): string {
  return `${vaultPath(vault)}/environments/${encodeURIComponent(environment)}/secret-requests`;
}

export function archiveSecretRequestPath(target: SecretTarget, id: string): string {
  return `${secretRequestsPath(target)}/${encodeURIComponent(id)}/archive`;
}

export function serviceTokensPath({ vault, environment }: SecretTarget): string {
  return `${vaultPath(vault)}/environments/${encodeURIComponent(environment)}/operator/tokens`;
}

export function serviceTokenPath(target: SecretTarget, id: string): string {
  return `${serviceTokensPath(target)}/${encodeURIComponent(id)}`;
}
