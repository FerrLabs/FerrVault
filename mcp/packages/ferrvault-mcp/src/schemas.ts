import { z } from 'zod';

const slug = z
  .string()
  .min(1)
  .max(40)
  .regex(/^[a-z0-9]+(-[a-z0-9]+)*$/, 'must be a lowercase slug (letters, digits, hyphens)');

export const vaultSlug = slug.describe('Vault slug, as listed by list_ferrvault_vaults');

export const environmentSlug = slug.describe(
  'Environment slug inside the vault, as listed by list_ferrvault_environments (e.g. prod)',
);

const newSlug = slug.min(2);

export const newVaultSlug = newSlug.describe(
  'Slug of the new vault, unique in the organization (e.g. ferrlabs-infra)',
);

export const newEnvironmentSlug = newSlug.describe(
  'Slug of the new environment, unique in the vault (e.g. staging)',
);

export const displayName = z.string().min(1).max(100).describe('Display name (e.g. Staging)');

export const secretName = z
  .string()
  .min(1)
  .max(200)
  .regex(
    /^[A-Za-z_][A-Za-z0-9_.]*$/,
    'must start with a letter or underscore, then letters, digits, underscores or dots',
  )
  .describe('Secret name (e.g. STRIPE_API_KEY)');

export const secretRequestId = z
  .uuid()
  .describe('Secret request id, as listed by list_ferrvault_secret_requests');

export const requestedName = z
  .string()
  .min(1)
  .max(200)
  .describe('Secret name a client requested, as listed by list_ferrvault_secret_requests');

export const serviceTokenId = z
  .uuid()
  .describe('Service token id, as listed by list_ferrvault_service_tokens');

export const serviceTokenName = z
  .string()
  .min(1)
  .max(100)
  .describe('Label shown for the token in FerrVault (e.g. k8s-operator-prod)');

export const vaultRole = z
  .enum(['viewer', 'writer', 'admin'])
  .describe('Role the token acts with on its vault environment');

export const expiresAt = z.iso
  .datetime({ offset: true })
  .describe('ISO-8601 instant after which the token stops working; omit for no expiry');
