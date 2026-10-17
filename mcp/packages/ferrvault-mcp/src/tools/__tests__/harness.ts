import { vi, beforeEach, afterEach, type Mock } from 'vitest';
import { z } from 'zod';
import type { McpServer } from '@ferrlabs/mcp-core';

export type Result = { content: { text: string }[]; isError?: boolean };
type Handler = (params: Record<string, unknown>) => Promise<Result>;

const handlers = new Map<string, Handler>();
const shapes = new Map<string, z.ZodRawShape>();
const server = {
  tool: (name: string, _description: string, shape: z.ZodRawShape, handler: Handler) => {
    handlers.set(name, handler);
    shapes.set(name, shape);
  },
} as unknown as McpServer;

export const mockFetch: Mock = vi.fn();
vi.stubGlobal('fetch', mockFetch);

export const API = 'https://api.ferrvault.ferrlabs';

export const metadata = {
  id: 's1',
  vault_id: 'v1',
  environment_id: 'e1',
  name: 'DB_PASSWORD',
  current_version: 1,
  tags: [],
  expires_at: null,
  expiry_action: 'notify',
  expired: false,
  created_at: '2026-10-07T00:00:00Z',
  updated_at: '2026-10-07T00:00:00Z',
};

export function respond(body: unknown, status = 200): Response {
  return {
    ok: status >= 200 && status < 300,
    status,
    text: () => Promise.resolve(body === undefined ? '' : JSON.stringify(body)),
  } as unknown as Response;
}

interface Sent {
  url: string;
  method: string;
  headers: Record<string, string>;
  body: Record<string, unknown> | undefined;
}

export function sent(index = 0): Sent {
  const [url, init] = mockFetch.mock.calls[index];
  return {
    url: String(url),
    method: init.method,
    headers: init.headers,
    body: init.body ? JSON.parse(init.body) : undefined,
  };
}

export function call(name: string, params: Record<string, unknown> = {}): Promise<Result> {
  const handler = handlers.get(name);
  if (!handler) throw new Error(`tool ${name} is not registered`);
  return handler(params);
}

export function accepts(name: string, params: Record<string, unknown>): boolean {
  const shape = shapes.get(name);
  if (!shape) throw new Error(`tool ${name} is not registered`);
  return z.object(shape).safeParse(params).success;
}

export function useRegisteredTools(): void {
  beforeEach(async () => {
    vi.resetModules();
    mockFetch.mockReset();
    handlers.clear();
    shapes.clear();
    process.env.FERRLABS_API_TOKEN = 'jwt-from-idp';
    process.env.FERRVAULT_API_URL = `${API}/`;
    process.env.FERRLABS_MCP_ALLOWED_API_HOSTS = 'api.ferrvault.ferrlabs';
    const { register } = await import('../../register.js');
    register(server);
  });

  afterEach(() => {
    delete process.env.FERRLABS_API_TOKEN;
    delete process.env.FERRVAULT_API_URL;
    delete process.env.FERRLABS_MCP_ALLOWED_API_HOSTS;
  });
}
