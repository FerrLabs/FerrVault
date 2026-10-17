import { describe, it, expect } from 'vitest';
import {
  API,
  accepts,
  call,
  metadata,
  mockFetch,
  respond,
  sent,
  useRegisteredTools,
} from './harness.js';

const SOURCE_SECRETS = `${API}/vaults/infra/environments/prod/secrets`;
const TARGET_SECRETS = `${API}/vaults/apps/environments/staging/secrets`;
const VALUE = 'pa$$w0rd-from-prod';

const source = { vault: 'infra', environment: 'prod', name: 'DB_PASSWORD' };
const target = { vault: 'apps', environment: 'staging', name: 'APP_DB_PASSWORD' };

function revealed(version = 7) {
  return respond({ ...metadata, current_version: version, value: VALUE });
}

describe('copy_ferrvault_secret', () => {
  useRegisteredTools();

  it('reveals the source, then creates the target with the same value', async () => {
    mockFetch.mockResolvedValueOnce(revealed()).mockResolvedValueOnce(respond(metadata));
    const result = await call('copy_ferrvault_secret', { source, target });

    expect(mockFetch).toHaveBeenCalledTimes(2);
    expect(sent(0)).toMatchObject({ method: 'GET', url: `${SOURCE_SECRETS}/DB_PASSWORD` });
    expect(sent(1)).toMatchObject({ method: 'POST', url: TARGET_SECRETS });
    expect(sent(1).body).toEqual({ name: 'APP_DB_PASSWORD', value: VALUE });
    expect(JSON.parse(result.content[0].text)).toEqual({
      outcome: 'created',
      target,
      source_version: 7,
      target_version: 1,
    });
  });

  it('never returns the copied value', async () => {
    mockFetch.mockResolvedValueOnce(revealed()).mockResolvedValueOnce(respond(metadata));
    const result = await call('copy_ferrvault_secret', { source, target });

    expect(result.isError).toBeUndefined();
    expect(JSON.stringify(result)).not.toContain(VALUE);
  });

  it('defaults the target name to the source name', async () => {
    mockFetch.mockResolvedValueOnce(revealed()).mockResolvedValueOnce(respond(metadata));
    const result = await call('copy_ferrvault_secret', {
      source,
      target: { vault: 'infra', environment: 'staging' },
    });

    expect(sent(1).url).toBe(`${API}/vaults/infra/environments/staging/secrets`);
    expect(sent(1).body?.name).toBe('DB_PASSWORD');
    expect(JSON.parse(result.content[0].text).target).toEqual({
      vault: 'infra',
      environment: 'staging',
      name: 'DB_PASSWORD',
    });
  });

  it('stores a new version when the target already exists', async () => {
    mockFetch
      .mockResolvedValueOnce(revealed())
      .mockResolvedValueOnce(respond({ code: 'SECRET_EXISTS', error: 'exists' }, 409))
      .mockResolvedValueOnce(respond({ ...metadata, current_version: 4 }));
    const result = await call('copy_ferrvault_secret', { source, target });

    expect(sent(2)).toMatchObject({ method: 'PUT', url: `${TARGET_SECRETS}/APP_DB_PASSWORD` });
    expect(sent(2).body).toEqual({ value: VALUE });
    expect(JSON.parse(result.content[0].text)).toMatchObject({
      outcome: 'versioned',
      source_version: 7,
      target_version: 4,
    });
  });

  it.each([
    ['an explicit name', { ...source }],
    ['the defaulted name', { vault: 'infra', environment: 'prod' }],
  ])('refuses a target equal to the source with %s', async (_label, to) => {
    const result = await call('copy_ferrvault_secret', { source, target: to });

    expect(mockFetch).not.toHaveBeenCalled();
    expect(result.isError).toBe(true);
    expect(result.content[0].text).toBe('source and target are the same secret');
  });

  it('copies between environments under the same name', async () => {
    mockFetch.mockResolvedValueOnce(revealed()).mockResolvedValueOnce(respond(metadata));
    const result = await call('copy_ferrvault_secret', {
      source,
      target: { ...source, environment: 'dev' },
    });

    expect(result.isError).toBeUndefined();
    expect(sent(1).url).toBe(`${API}/vaults/infra/environments/dev/secrets`);
  });

  it('redacts the value from a write error that echoes it', async () => {
    mockFetch
      .mockResolvedValueOnce(revealed())
      .mockResolvedValueOnce(respond({ code: 'VALIDATION', error: `bad value ${VALUE}` }, 400));
    const result = await call('copy_ferrvault_secret', { source, target });

    expect(result.isError).toBe(true);
    expect(JSON.stringify(result)).not.toContain(VALUE);
    expect(result.content[0].text).toBe(
      'FerrVault API error (HTTP 400 VALIDATION): bad value [redacted]',
    );
  });

  it('redacts the value from a non-JSON error body', async () => {
    mockFetch.mockResolvedValueOnce(revealed()).mockResolvedValueOnce({
      ok: false,
      status: 502,
      text: () => Promise.resolve(`<html>upstream saw ${VALUE}</html>`),
    } as unknown as Response);
    const result = await call('copy_ferrvault_secret', { source, target });

    expect(result.isError).toBe(true);
    expect(JSON.stringify(result)).not.toContain(VALUE);
  });

  it('writes nothing when the source cannot be revealed', async () => {
    mockFetch.mockResolvedValue(respond({ code: 'AUTH_FORBIDDEN', error: 'viewer role' }, 403));
    const result = await call('copy_ferrvault_secret', { source, target });

    expect(mockFetch).toHaveBeenCalledTimes(1);
    expect(result.content[0].text).toBe(
      'FerrVault API error (HTTP 403 AUTH_FORBIDDEN): viewer role',
    );
  });

  it('forwards the caller bearer and the version header on the read and the write', async () => {
    const { runWithAuthContext } = await import('@ferrlabs/mcp-core');
    mockFetch.mockResolvedValueOnce(revealed()).mockResolvedValueOnce(respond(metadata));
    await runWithAuthContext({ bearerToken: 'caller-token' }, () =>
      call('copy_ferrvault_secret', { source, target }),
    );

    for (const index of [0, 1]) {
      expect(sent(index).headers['Authorization']).toBe('Bearer caller-token');
      expect(sent(index).headers['x-ferrvault-api-version']).toBe('2026-08-04');
    }
  });

  it.each([
    ['a traversing source vault', { source: { ...source, vault: '..' }, target }],
    ['a slash in the target name', { source, target: { ...target, name: 'a/b' } }],
    ['an uppercase target environment', { source, target: { ...target, environment: 'Prod' } }],
    ['a missing source name', { source: { vault: 'infra', environment: 'prod' }, target }],
  ])('rejects %s', (_label, params) => {
    expect(accepts('copy_ferrvault_secret', params)).toBe(false);
  });

  it('accepts a target without a name', () => {
    expect(
      accepts('copy_ferrvault_secret', { source, target: { vault: 'apps', environment: 'dev' } }),
    ).toBe(true);
  });
});
