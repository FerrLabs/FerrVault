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

const TOKENS = `${API}/vaults/infra/environments/prod/operator/tokens`;
const STORE_SECRETS = `${API}/vaults/infra/environments/ci/secrets`;
const SAT_ID = '0b0e3c4e-6c2f-4f0e-9a51-7a1f2d1c9e01';
const SAT = 'fvsat_Zm9vYmFyYmF6cXV4c2VjcmV0dG9rZW5ib2R5MTIzNDU2';

const row = {
  id: SAT_ID,
  label: 'k8s-operator-prod',
  role: 'viewer',
  token_preview: 'Zm9vYmFy',
  created_by: 'u1',
  created_at: '2026-10-07T00:00:00Z',
  expires_at: '2027-01-01T00:00:00Z',
  last_used_at: null,
};

const tokenMetadata = {
  id: SAT_ID,
  name: 'k8s-operator-prod',
  vault: 'infra',
  environment: 'prod',
  role: 'viewer',
  created_by: 'u1',
  created_at: '2026-10-07T00:00:00Z',
  expires_at: '2027-01-01T00:00:00Z',
  last_used_at: null,
};

const store = { vault: 'infra', environment: 'ci', name: 'OPERATOR_TOKEN' };

const create = {
  vault: 'infra',
  environment: 'prod',
  name: 'k8s-operator-prod',
  role: 'viewer',
  expires_at: '2027-01-01T00:00:00Z',
  store,
};

describe('list_ferrvault_service_tokens', () => {
  useRegisteredTools();

  it('lists metadata from the environment operator token route', async () => {
    mockFetch.mockResolvedValue(respond([row]));
    const result = await call('list_ferrvault_service_tokens', {
      vault: 'infra',
      environment: 'prod',
    });

    expect(sent()).toMatchObject({ method: 'GET', url: TOKENS });
    expect(JSON.parse(result.content[0].text)).toEqual([tokenMetadata]);
  });

  it('drops the token, its hash and its preview even if the API sends them', async () => {
    mockFetch.mockResolvedValue(
      respond([{ ...row, token: SAT, token_hash: '$argon2id$v=19$hash', allowed_cidrs: [] }]),
    );
    const result = await call('list_ferrvault_service_tokens', {
      vault: 'infra',
      environment: 'prod',
    });

    const text = JSON.stringify(result);
    expect(text).not.toContain(SAT);
    expect(text).not.toContain('argon2id');
    expect(text).not.toContain(row.token_preview);
  });
});

describe('create_ferrvault_service_token', () => {
  useRegisteredTools();

  it('creates the token, then writes its value into the store secret', async () => {
    mockFetch
      .mockResolvedValueOnce(respond({ ...row, token: SAT }))
      .mockResolvedValueOnce(respond({ ...metadata, name: 'OPERATOR_TOKEN' }));
    const result = await call('create_ferrvault_service_token', create);

    expect(mockFetch).toHaveBeenCalledTimes(2);
    expect(sent(0)).toMatchObject({ method: 'POST', url: TOKENS });
    expect(sent(0).body).toEqual({
      label: 'k8s-operator-prod',
      role: 'viewer',
      expires_at: '2027-01-01T00:00:00Z',
    });
    expect(sent(1)).toMatchObject({ method: 'POST', url: STORE_SECRETS });
    expect(sent(1).body).toEqual({ name: 'OPERATOR_TOKEN', value: SAT });
    expect(JSON.parse(result.content[0].text)).toEqual({
      token: tokenMetadata,
      stored: { outcome: 'created', ...store, version: 1 },
    });
  });

  it('never returns the token value', async () => {
    mockFetch
      .mockResolvedValueOnce(respond({ ...row, token: SAT }))
      .mockResolvedValueOnce(respond(metadata));
    const result = await call('create_ferrvault_service_token', create);

    expect(result.isError).toBeUndefined();
    expect(JSON.stringify(result)).not.toContain(SAT);
  });

  it('omits the expiry when none is given', async () => {
    mockFetch
      .mockResolvedValueOnce(respond({ ...row, expires_at: null, token: SAT }))
      .mockResolvedValueOnce(respond(metadata));
    const { expires_at: _omitted, ...withoutExpiry } = create;
    await call('create_ferrvault_service_token', withoutExpiry);

    expect(sent(0).body).toEqual({ label: 'k8s-operator-prod', role: 'viewer' });
  });

  it('stores a new version when the store secret exists', async () => {
    mockFetch
      .mockResolvedValueOnce(respond({ ...row, token: SAT }))
      .mockResolvedValueOnce(respond({ code: 'SECRET_EXISTS', error: 'exists' }, 409))
      .mockResolvedValueOnce(respond({ ...metadata, current_version: 5 }));
    const result = await call('create_ferrvault_service_token', create);

    expect(sent(2)).toMatchObject({ method: 'PUT', url: `${STORE_SECRETS}/OPERATOR_TOKEN` });
    expect(sent(2).body).toEqual({ value: SAT });
    expect(JSON.parse(result.content[0].text).stored).toEqual({
      outcome: 'versioned',
      ...store,
      version: 5,
    });
  });

  it('revokes the new token when storing it fails, without leaking it', async () => {
    mockFetch
      .mockResolvedValueOnce(respond({ ...row, token: SAT }))
      .mockResolvedValueOnce(respond({ code: 'VALIDATION', error: `rejected ${SAT}` }, 400))
      .mockResolvedValueOnce(respond(undefined, 204));
    const result = await call('create_ferrvault_service_token', create);

    expect(sent(2)).toMatchObject({ method: 'DELETE', url: `${TOKENS}/${SAT_ID}` });
    expect(result.isError).toBe(true);
    expect(JSON.stringify(result)).not.toContain(SAT);
    expect(result.content[0].text).toBe(
      `storing service token ${SAT_ID} failed (FerrVault API error (HTTP 400 VALIDATION): rejected [redacted]); the token was revoked`,
    );
  });

  it('says so when the compensating revoke fails too', async () => {
    mockFetch
      .mockResolvedValueOnce(respond({ ...row, token: SAT }))
      .mockResolvedValueOnce(respond({ code: 'AUTH_FORBIDDEN', error: 'viewer role' }, 403))
      .mockResolvedValueOnce(respond({ code: 'INTERNAL', error: 'boom' }, 500));
    const result = await call('create_ferrvault_service_token', create);

    expect(result.isError).toBe(true);
    expect(result.content[0].text).toContain('revoking it failed too, revoke it by id');
  });

  it('stores nothing when the API refuses to create the token', async () => {
    mockFetch.mockResolvedValue(respond({ code: 'AUTH_FORBIDDEN', error: 'admin only' }, 403));
    const result = await call('create_ferrvault_service_token', create);

    expect(mockFetch).toHaveBeenCalledTimes(1);
    expect(result.content[0].text).toBe(
      'FerrVault API error (HTTP 403 AUTH_FORBIDDEN): admin only',
    );
  });

  it('forwards the caller bearer and the version header on every call', async () => {
    const { runWithAuthContext } = await import('@ferrlabs/mcp-core');
    mockFetch
      .mockResolvedValueOnce(respond({ ...row, token: SAT }))
      .mockResolvedValueOnce(respond({ code: 'VALIDATION', error: 'no' }, 400))
      .mockResolvedValueOnce(respond(undefined, 204));
    await runWithAuthContext({ bearerToken: 'caller-token' }, () =>
      call('create_ferrvault_service_token', create),
    );

    expect(mockFetch).toHaveBeenCalledTimes(3);
    for (const index of [0, 1, 2]) {
      expect(sent(index).headers['Authorization']).toBe('Bearer caller-token');
      expect(sent(index).headers['x-ferrvault-api-version']).toBe('2026-08-04');
    }
  });

  it.each([
    ['an unknown role', { ...create, role: 'owner' }],
    ['a non ISO expiry', { ...create, expires_at: 'next week' }],
    ['an empty name', { ...create, name: '' }],
    ['a name over 100 characters', { ...create, name: 'x'.repeat(101) }],
    ['a traversing store vault', { ...create, store: { ...store, vault: '..' } }],
    ['a store without a name', { ...create, store: { vault: 'infra', environment: 'ci' } }],
  ])('rejects %s', (_label, params) => {
    expect(accepts('create_ferrvault_service_token', params)).toBe(false);
  });

  it('accepts a request without expiry', () => {
    const { expires_at: _omitted, ...withoutExpiry } = create;
    expect(accepts('create_ferrvault_service_token', withoutExpiry)).toBe(true);
  });
});

describe('revoke_ferrvault_service_token', () => {
  useRegisteredTools();

  it('revokes with DELETE on the token route', async () => {
    mockFetch.mockResolvedValue(respond(undefined, 204));
    const result = await call('revoke_ferrvault_service_token', {
      vault: 'infra',
      environment: 'prod',
      id: SAT_ID,
    });

    expect(mockFetch).toHaveBeenCalledTimes(1);
    expect(sent()).toMatchObject({ method: 'DELETE', url: `${TOKENS}/${SAT_ID}` });
    expect(sent().body).toBeUndefined();
    expect(JSON.parse(result.content[0].text)).toEqual({
      revoked: SAT_ID,
      vault: 'infra',
      environment: 'prod',
    });
  });

  it('maps an unknown token to a tool error', async () => {
    mockFetch.mockResolvedValue(respond({ code: 'TOKEN_NOT_FOUND', error: 'no such token' }, 404));
    const result = await call('revoke_ferrvault_service_token', {
      vault: 'infra',
      environment: 'prod',
      id: SAT_ID,
    });

    expect(result.isError).toBe(true);
    expect(result.content[0].text).toBe(
      'FerrVault API error (HTTP 404 TOKEN_NOT_FOUND): no such token',
    );
  });

  it.each(['..', `${SAT_ID}/..`, 'not-a-uuid'])('rejects %j as a token id', (id) => {
    expect(
      accepts('revoke_ferrvault_service_token', { vault: 'infra', environment: 'prod', id }),
    ).toBe(false);
  });
});
