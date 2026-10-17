import { describe, it, expect } from 'vitest';
import { API, call, metadata, mockFetch, respond, sent, useRegisteredTools } from './harness.js';

const SECRETS = `${API}/vaults/infra/environments/prod/secrets`;

const target = { vault: 'infra', environment: 'prod', name: 'DB_PASSWORD' };

const REQUESTS = `${API}/vaults/infra/environments/prod/secret-requests`;
const PENDING_ID = '0b0e3c4e-6c2f-4f0e-9a51-7a1f2d1c9e01';
const ARCHIVED_ID = '5d7c1e2a-3b4f-4a6e-8c9d-0e1f2a3b4c5d';

function secretRequest(id: string, name: string, state: string) {
  return {
    id,
    vault_id: 'v1',
    environment_id: 'e1',
    name,
    state,
    first_requested_at: '2026-10-01T00:00:00Z',
    last_requested_at: '2026-10-07T00:00:00Z',
    request_count: 4,
    last_requester_kind: 'operator_token',
    last_requester_id: 'op-token-1',
    fulfilled_at: null,
  };
}

describe('ferrvault api tools', () => {
  useRegisteredTools();

  it('sends the bearer token and pins the contract version on every call', async () => {
    mockFetch.mockResolvedValue(respond([]));
    await call('list_ferrvault_vaults');

    const { url, headers } = sent();
    expect(url).toBe(`${API}/vaults`);
    expect(headers['Authorization']).toBe('Bearer jwt-from-idp');
    expect(headers['x-ferrvault-api-version']).toBe('2026-08-04');
  });

  it('forwards the bearer of the HTTP request rather than any stored token', async () => {
    const { runWithAuthContext } = await import('@ferrlabs/mcp-core');
    mockFetch.mockResolvedValue(respond([]));
    await runWithAuthContext({ bearerToken: 'caller-token' }, () =>
      call('list_ferrvault_environments', { vault: 'infra' }),
    );

    expect(sent().url).toBe(`${API}/vaults/infra/environments`);
    expect(sent().headers['Authorization']).toBe('Bearer caller-token');
  });

  it('refuses to send the token to a host outside the allowlist', async () => {
    process.env.FERRLABS_MCP_ALLOWED_API_HOSTS = '';
    const result = await call('list_ferrvault_vaults');

    expect(mockFetch).not.toHaveBeenCalled();
    expect(result.isError).toBe(true);
    expect(result.content[0].text).toContain('not an allowed FerrLabs API host');
  });

  it('never returns a value from list_ferrvault_secrets, even if the API sends one', async () => {
    mockFetch.mockResolvedValue(respond([{ ...metadata, value: 'leaked' }]));
    const result = await call('list_ferrvault_secrets', { vault: 'infra', environment: 'prod' });

    expect(sent().url).toBe(SECRETS);
    expect(result.content[0].text).not.toContain('leaked');
    expect(JSON.parse(result.content[0].text)[0].name).toBe('DB_PASSWORD');
  });

  it('reveals a secret from its per-name route', async () => {
    mockFetch.mockResolvedValue(respond({ ...metadata, value: 'hunter2' }));
    const result = await call('reveal_ferrvault_secret', target);

    expect(sent().url).toBe(`${SECRETS}/DB_PASSWORD`);
    expect(sent().method).toBe('GET');
    expect(JSON.parse(result.content[0].text).value).toBe('hunter2');
  });

  it('deletes with DELETE on the per-name route', async () => {
    mockFetch.mockResolvedValue(respond(undefined, 204));
    const result = await call('delete_ferrvault_secret', target);

    expect(sent().method).toBe('DELETE');
    expect(sent().url).toBe(`${SECRETS}/DB_PASSWORD`);
    expect(result.isError).toBeUndefined();
  });

  it('set_ferrvault_secret creates the secret when it does not exist', async () => {
    mockFetch.mockResolvedValue(respond(metadata));
    const result = await call('set_ferrvault_secret', { ...target, value: 'v1' });

    expect(mockFetch).toHaveBeenCalledTimes(1);
    expect(sent().method).toBe('POST');
    expect(sent().url).toBe(SECRETS);
    expect(sent().body).toEqual({ name: 'DB_PASSWORD', value: 'v1' });
    expect(JSON.parse(result.content[0].text).outcome).toBe('created');
  });

  it('set_ferrvault_secret stores a new version when the name is taken', async () => {
    mockFetch
      .mockResolvedValueOnce(respond({ code: 'SECRET_EXISTS', error: 'exists' }, 409))
      .mockResolvedValueOnce(respond({ ...metadata, current_version: 2 }));
    const result = await call('set_ferrvault_secret', { ...target, value: 'v2' });

    expect(sent(1).method).toBe('PUT');
    expect(sent(1).url).toBe(`${SECRETS}/DB_PASSWORD`);
    expect(sent(1).body).toEqual({ value: 'v2' });
    const parsed = JSON.parse(result.content[0].text);
    expect(parsed.outcome).toBe('rotated');
    expect(parsed.secret.current_version).toBe(2);
  });

  it('does not overwrite on a conflict that is not an existing secret', async () => {
    mockFetch.mockResolvedValue(respond({ code: 'VAULT_REKEYING', error: 'busy' }, 409));
    const result = await call('set_ferrvault_secret', { ...target, value: 'v2' });

    expect(mockFetch).toHaveBeenCalledTimes(1);
    expect(result.isError).toBe(true);
    expect(result.content[0].text).toBe('FerrVault API error (HTTP 409 VAULT_REKEYING): busy');
  });

  it('maps an API refusal to a tool error carrying status and code', async () => {
    mockFetch.mockResolvedValue(respond({ code: 'AUTH_FORBIDDEN', error: 'viewer role' }, 403));
    const result = await call('reveal_ferrvault_secret', target);

    expect(result.isError).toBe(true);
    expect(result.content[0].text).toBe(
      'FerrVault API error (HTTP 403 AUTH_FORBIDDEN): viewer role',
    );
  });

  it('generate_ferrvault_secret stores a random value and never returns it', async () => {
    mockFetch.mockResolvedValue(respond(metadata));
    const result = await call('generate_ferrvault_secret', {
      ...target,
      length: 64,
      charset: 'hex',
    });

    const stored = sent().body?.value;
    expect(stored).toMatch(/^[0-9a-f]{64}$/);
    expect(result.isError).toBeUndefined();
    expect(result.content[0].text).not.toContain(String(stored));
    expect(JSON.parse(result.content[0].text)).toMatchObject({
      outcome: 'created',
      length: 64,
      charset: 'hex',
    });
  });

  it('generate_ferrvault_secret rotates an existing secret with a fresh value', async () => {
    mockFetch
      .mockResolvedValueOnce(respond({ code: 'SECRET_EXISTS', error: 'exists' }, 409))
      .mockResolvedValueOnce(respond({ ...metadata, current_version: 3 }));
    const result = await call('generate_ferrvault_secret', target);

    expect(sent(1).method).toBe('PUT');
    expect(sent(1).body?.value).toBe(sent(0).body?.value);
    expect(sent(1).body?.value).toMatch(/^[A-Za-z0-9]{48}$/);
    expect(JSON.parse(result.content[0].text).outcome).toBe('rotated');
  });

  it('generate_ferrvault_secret redacts the value from an error that echoes it', async () => {
    mockFetch.mockImplementation((_url: string, init: { body: string }) => {
      const { value } = JSON.parse(init.body);
      return Promise.resolve(respond({ code: 'VALIDATION', error: `bad value ${value}` }, 400));
    });
    const result = await call('generate_ferrvault_secret', target);

    const stored = String(sent().body?.value);
    expect(result.isError).toBe(true);
    expect(result.content[0].text).not.toContain(stored);
    expect(result.content[0].text).toBe(
      'FerrVault API error (HTTP 400 VALIDATION): bad value [redacted]',
    );
  });

  it('generates distinct values', async () => {
    mockFetch.mockResolvedValue(respond(metadata));
    await call('generate_ferrvault_secret', target);
    await call('generate_ferrvault_secret', target);

    expect(sent(0).body?.value).not.toBe(sent(1).body?.value);
  });
  it('lists secret requests with counts, timestamps and requester kind', async () => {
    mockFetch.mockResolvedValue(
      respond({ requests: [secretRequest(PENDING_ID, 'STRIPE_KEY', 'pending')] }),
    );
    const result = await call('list_ferrvault_secret_requests', {
      vault: 'infra',
      environment: 'prod',
    });

    expect(sent().url).toBe(REQUESTS);
    expect(sent().method).toBe('GET');
    expect(sent().headers['x-ferrvault-api-version']).toBe('2026-08-04');
    expect(JSON.parse(result.content[0].text)).toEqual([
      {
        id: PENDING_ID,
        name: 'STRIPE_KEY',
        state: 'pending',
        request_count: 4,
        first_requested_at: '2026-10-01T00:00:00Z',
        last_requested_at: '2026-10-07T00:00:00Z',
        fulfilled_at: null,
        last_requester_kind: 'operator_token',
      },
    ]);
  });

  it('archives a request by id with a bodiless POST on its archive route', async () => {
    mockFetch.mockResolvedValue(respond(undefined, 204));
    const result = await call('archive_ferrvault_secret_request', {
      vault: 'infra',
      environment: 'prod',
      id: PENDING_ID,
    });

    expect(mockFetch).toHaveBeenCalledTimes(1);
    expect(sent().method).toBe('POST');
    expect(sent().url).toBe(`${REQUESTS}/${PENDING_ID}/archive`);
    expect(sent().body).toBeUndefined();
    expect(JSON.parse(result.content[0].text)).toMatchObject({
      archived: PENDING_ID,
      state: 'archived',
    });
  });

  it('resolves a name to the id of its pending request before archiving', async () => {
    mockFetch
      .mockResolvedValueOnce(
        respond({
          requests: [
            secretRequest(ARCHIVED_ID, 'STRIPE_KEY', 'archived'),
            secretRequest('9f8e7d6c-5b4a-4321-8fed-cba987654321', 'OTHER', 'pending'),
            secretRequest(PENDING_ID, 'STRIPE_KEY', 'pending'),
          ],
        }),
      )
      .mockResolvedValueOnce(respond(undefined, 204));
    const result = await call('archive_ferrvault_secret_request', {
      vault: 'infra',
      environment: 'prod',
      name: 'STRIPE_KEY',
    });

    expect(sent(0).url).toBe(REQUESTS);
    expect(sent(1).method).toBe('POST');
    expect(sent(1).url).toBe(`${REQUESTS}/${PENDING_ID}/archive`);
    expect(JSON.parse(result.content[0].text)).toMatchObject({
      archived: PENDING_ID,
      name: 'STRIPE_KEY',
    });
  });

  it('fails without archiving anything when no pending request has the name', async () => {
    mockFetch.mockResolvedValue(
      respond({ requests: [secretRequest(ARCHIVED_ID, 'STRIPE_KEY', 'archived')] }),
    );
    const result = await call('archive_ferrvault_secret_request', {
      vault: 'infra',
      environment: 'prod',
      name: 'STRIPE_KEY',
    });

    expect(mockFetch).toHaveBeenCalledTimes(1);
    expect(result.isError).toBe(true);
    expect(result.content[0].text).toBe('no pending secret request named STRIPE_KEY in infra/prod');
  });

  it.each([
    ['neither', {}],
    ['both', { id: PENDING_ID, name: 'STRIPE_KEY' }],
  ])('refuses an archive with %s of id and name', async (_label, ref) => {
    const result = await call('archive_ferrvault_secret_request', {
      vault: 'infra',
      environment: 'prod',
      ...ref,
    });

    expect(mockFetch).not.toHaveBeenCalled();
    expect(result.isError).toBe(true);
    expect(result.content[0].text).toBe('pass exactly one of id or name');
  });

  it('maps an already archived request to a tool error', async () => {
    mockFetch.mockResolvedValue(
      respond({ code: 'SECRET_REQUEST_NOT_FOUND', error: 'request already archived' }, 404),
    );
    const result = await call('archive_ferrvault_secret_request', {
      vault: 'infra',
      environment: 'prod',
      id: PENDING_ID,
    });

    expect(result.isError).toBe(true);
    expect(result.content[0].text).toBe(
      'FerrVault API error (HTTP 404 SECRET_REQUEST_NOT_FOUND): request already archived',
    );
  });

  it('forwards the caller bearer and the version header on both archive-by-name calls', async () => {
    const { runWithAuthContext } = await import('@ferrlabs/mcp-core');
    mockFetch
      .mockResolvedValueOnce(
        respond({ requests: [secretRequest(PENDING_ID, 'STRIPE_KEY', 'pending')] }),
      )
      .mockResolvedValueOnce(respond(undefined, 204));
    await runWithAuthContext({ bearerToken: 'caller-token' }, () =>
      call('archive_ferrvault_secret_request', {
        vault: 'infra',
        environment: 'prod',
        name: 'STRIPE_KEY',
      }),
    );

    for (const index of [0, 1]) {
      expect(sent(index).headers['Authorization']).toBe('Bearer caller-token');
      expect(sent(index).headers['x-ferrvault-api-version']).toBe('2026-08-04');
    }
  });
});
