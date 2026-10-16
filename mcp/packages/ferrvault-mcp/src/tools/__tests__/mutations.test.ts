import { describe, it, expect, vi, beforeEach } from 'vitest';
import type { ZodTypeAny } from 'zod';
import type { McpServer } from '@ferrlabs/mcp-core';

process.env.FERRLABS_API_TOKEN = 'test-token';

type Handler = (params: Record<string, unknown>) => Promise<{ content: { text?: string }[] }>;

const handlers = new Map<string, Handler>();
const schemas = new Map<string, Record<string, ZodTypeAny>>();
const mockServer = {
  tool: vi.fn(
    (name: string, _desc: string, schema: Record<string, ZodTypeAny>, handler: Handler) => {
      handlers.set(name, handler);
      schemas.set(name, schema);
    },
  ),
} as unknown as McpServer;

const mockFetch = vi.fn();
vi.stubGlobal('fetch', mockFetch);

function ok(body: unknown = { id: 's1' }): Response {
  return {
    ok: true,
    status: 200,
    text: () => Promise.resolve(JSON.stringify(body)),
    json: () => Promise.resolve(body),
  } as unknown as Response;
}

function lastCall(): { url: string; method: string; body: unknown } {
  const [url, init] = mockFetch.mock.calls[0];
  return {
    url: String(url),
    method: init.method ?? 'GET',
    body: init.body ? JSON.parse(init.body) : undefined,
  };
}

const VAULT_BASE = 'https://api.ferrlabs.com/orgs/acme/projects/web/vaults/v1';

describe('ferrvault write tools', () => {
  beforeEach(async () => {
    vi.clearAllMocks();
    handlers.clear();
    schemas.clear();
    mockFetch.mockResolvedValue(ok());
    const { registerSecretMutationTools } = await import('../secret-mutations.js');
    const { registerVaultMutationTools } = await import('../vaults.js');
    registerSecretMutationTools(mockServer);
    registerVaultMutationTools(mockServer);
  });

  it('create_secret posts to the vault secrets collection on the FerrLabs API', async () => {
    await handlers.get('create_secret')!({
      org_slug: 'acme',
      project_slug: 'web',
      vault_id: 'v1',
      name: 'STRIPE_API_KEY',
      value: 's3cret',
    });

    const call = lastCall();
    expect(call.url).toBe(`${VAULT_BASE}/secrets`);
    expect(call.method).toBe('POST');
    expect(call.body).toEqual({ name: 'STRIPE_API_KEY', value: 's3cret', description: null });
  });

  it('update_secret patches only the fields that were passed', async () => {
    await handlers.get('update_secret')!({
      org_slug: 'acme',
      project_slug: 'web',
      vault_id: 'v1',
      secret_id: 'sec1',
      description: 'rotated by hand',
    });

    const call = lastCall();
    expect(call.url).toBe(`${VAULT_BASE}/secrets/sec1`);
    expect(call.method).toBe('PATCH');
    expect(call.body).toEqual({ description: 'rotated by hand' });
  });

  it('rotate_secret posts to the rotate sub-resource, not the secret itself', async () => {
    await handlers.get('rotate_secret')!({
      org_slug: 'acme',
      project_slug: 'web',
      vault_id: 'v1',
      secret_id: 'sec1',
      value: 'new-value',
    });

    const call = lastCall();
    expect(call.url).toBe(`${VAULT_BASE}/secrets/sec1/rotate`);
    expect(call.method).toBe('POST');
    expect(call.body).toEqual({ value: 'new-value' });
  });

  it('delete_secret uses DELETE and names the id it removed', async () => {
    const result = await handlers.get('delete_secret')!({
      org_slug: 'acme',
      project_slug: 'web',
      vault_id: 'v1',
      secret_id: 'sec1',
    });

    const call = lastCall();
    expect(call.url).toBe(`${VAULT_BASE}/secrets/sec1`);
    expect(call.method).toBe('DELETE');
    expect(result.content[0].text).toContain('sec1');
  });

  it('delete_vault targets the vault itself rather than a secret under it', async () => {
    const result = await handlers.get('delete_vault')!({
      org_slug: 'acme',
      project_slug: 'web',
      vault_id: 'v1',
    });

    const call = lastCall();
    expect(call.url).toBe(VAULT_BASE);
    expect(call.method).toBe('DELETE');
    expect(result.content[0].text).toContain('v1');
  });

  it('escapes path segments so a slug cannot climb the path', async () => {
    await handlers.get('delete_secret')!({
      org_slug: 'acme',
      project_slug: 'web',
      vault_id: 'v1',
      secret_id: '../../../orgs',
    });

    const call = lastCall();
    expect(call.url).toBe(`${VAULT_BASE}/secrets/..%2F..%2F..%2Forgs`);
    expect(call.url).not.toContain('/../');
  });

  it('rejects a secret name that is not SCREAMING_SNAKE_CASE', () => {
    const name = schemas.get('create_secret')!.name;
    expect(name.safeParse('STRIPE_API_KEY').success).toBe(true);
    expect(name.safeParse('stripe_api_key').success).toBe(false);
    expect(name.safeParse('Stripe-Api-Key').success).toBe(false);
  });
});
