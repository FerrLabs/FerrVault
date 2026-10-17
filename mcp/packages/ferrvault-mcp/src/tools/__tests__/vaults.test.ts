import { describe, it, expect } from 'vitest';
import { API, accepts, call, mockFetch, respond, sent, useRegisteredTools } from './harness.js';

const vault = {
  id: 'v1',
  slug: 'ferrlabs-infra',
  name: 'FerrLabs infra',
  summary: null,
  kek_key_id: 'vault-transit://ferrvault-org-ferrlabs-infra',
  kek_generation: 1,
  kek_managed: false,
  kek_dormant_at: null,
  kek_protection: null,
  created_at: '2026-10-10T00:00:00Z',
  updated_at: '2026-10-10T00:00:00Z',
};

const environment = {
  id: 'e2',
  vault_id: 'v1',
  slug: 'staging',
  name: 'Staging',
  summary: null,
  created_at: '2026-10-10T00:00:00Z',
  updated_at: '2026-10-10T00:00:00Z',
};

describe('vault and environment creation', () => {
  useRegisteredTools();

  it('creates a vault with a POST of slug and name only', async () => {
    mockFetch.mockResolvedValue(respond(vault));
    const result = await call('create_ferrvault_vault', {
      slug: 'ferrlabs-infra',
      name: 'FerrLabs infra',
    });

    expect(mockFetch).toHaveBeenCalledTimes(1);
    expect(sent().method).toBe('POST');
    expect(sent().url).toBe(`${API}/vaults`);
    expect(sent().body).toEqual({ slug: 'ferrlabs-infra', name: 'FerrLabs infra' });
    expect(sent().headers['x-ferrvault-api-version']).toBe('2026-08-04');
    expect(JSON.parse(result.content[0].text)).toMatchObject({ id: 'v1', slug: 'ferrlabs-infra' });
  });

  it('maps a taken vault slug to a tool error', async () => {
    mockFetch.mockResolvedValue(
      respond({ code: 'VAULT_EXISTS', error: 'a vault with that slug already exists' }, 409),
    );
    const result = await call('create_ferrvault_vault', { slug: 'infra', name: 'Infra' });

    expect(result.isError).toBe(true);
    expect(result.content[0].text).toBe(
      'FerrVault API error (HTTP 409 VAULT_EXISTS): a vault with that slug already exists',
    );
  });

  it('creates an environment with a POST on the vault environments route', async () => {
    mockFetch.mockResolvedValue(respond(environment));
    const result = await call('create_ferrvault_environment', {
      vault: 'ferrlabs-infra',
      slug: 'staging',
      name: 'Staging',
    });

    expect(sent().method).toBe('POST');
    expect(sent().url).toBe(`${API}/vaults/ferrlabs-infra/environments`);
    expect(sent().body).toEqual({ slug: 'staging', name: 'Staging' });
    expect(JSON.parse(result.content[0].text)).toMatchObject({ id: 'e2', slug: 'staging' });
  });

  it('maps a missing admin role to a tool error', async () => {
    mockFetch.mockResolvedValue(respond({ code: 'FORBIDDEN', error: 'requires admin' }, 403));
    const result = await call('create_ferrvault_environment', {
      vault: 'infra',
      slug: 'staging',
      name: 'Staging',
    });

    expect(result.isError).toBe(true);
    expect(result.content[0].text).toBe('FerrVault API error (HTTP 403 FORBIDDEN): requires admin');
  });

  it.each([
    ['a one-character slug', { slug: 'a', name: 'A' }],
    ['an uppercase slug', { slug: 'Infra', name: 'Infra' }],
    ['a path in the slug', { slug: 'a/b', name: 'Infra' }],
    ['a slug over 40 characters', { slug: 'a'.repeat(41), name: 'Infra' }],
    ['an empty name', { slug: 'infra', name: '' }],
    ['a name over 100 characters', { slug: 'infra', name: 'n'.repeat(101) }],
  ])('rejects %s, as the API does', (_label, params) => {
    expect(accepts('create_ferrvault_vault', params)).toBe(false);
    expect(accepts('create_ferrvault_environment', { vault: 'infra', ...params })).toBe(false);
  });

  it('accepts a two-character slug', () => {
    expect(accepts('create_ferrvault_vault', { slug: 'qa', name: 'QA' })).toBe(true);
    expect(
      accepts('create_ferrvault_environment', { vault: 'infra', slug: 'qa', name: 'QA' }),
    ).toBe(true);
  });
});
