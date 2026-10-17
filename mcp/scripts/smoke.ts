import { execFileSync, spawn, type ChildProcessWithoutNullStreams } from 'node:child_process';
import { fileURLToPath } from 'node:url';
import { dirname, resolve } from 'node:path';
import { existsSync, readFileSync } from 'node:fs';

const __dirname = dirname(fileURLToPath(import.meta.url));

interface ServerUnderTest {
  /** Directory under `packages/`. */
  pkg: string;
  /** `serverInfo.name` the binary must report. */
  serverName: string;
  /** A few tools that must be advertised. Not the full list: this is a boot check. */
  expectedTools: string[];
  /**
   * Tools to actually invoke. Only the servers with anonymous tools have any:
   * every other sub-MCP tool needs a credential.
   */
  liveCalls?: LiveCall[];
  /** URI templates the server must advertise, if it exposes resources at all. */
  expectedResourceTemplates?: string[];
  /** Prompt names the server must advertise, if it exposes prompts at all. */
  expectedPrompts?: string[];
}

interface LiveCall {
  tool: string;
  arguments?: Record<string, unknown>;
  check: (parsed: unknown) => boolean;
  describe: (parsed: unknown) => string;
}

const SERVERS: ServerUnderTest[] = [
  {
    pkg: 'ferrvault-mcp',
    serverName: 'ferrvault',
    expectedTools: [
      'list_ferrvault_vaults',
      'list_ferrvault_environments',
      'list_ferrvault_secrets',
      'reveal_ferrvault_secret',
      'set_ferrvault_secret',
      'generate_ferrvault_secret',
      'delete_ferrvault_secret',
      'list_ferrvault_secret_requests',
      'archive_ferrvault_secret_request',
      'copy_ferrvault_secret',
      'list_ferrvault_service_tokens',
      'create_ferrvault_service_token',
      'revoke_ferrvault_service_token',
    ],
  },
];

interface JsonRpcResponse {
  jsonrpc: '2.0';
  id: number;
  result?: unknown;
  error?: { code: number; message: string };
}

interface ContentBlock {
  type: string;
  text?: string;
}

interface ToolResult {
  content?: ContentBlock[];
  isError?: boolean;
}

interface Check {
  name: string;
  ok: boolean;
  detail: string;
}

const TIMEOUT_MS = 10_000;

function isRecord(value: unknown): value is Record<string, unknown> {
  return typeof value === 'object' && value !== null;
}

/**
 * One server subprocess speaking JSON-RPC over stdio.
 */
class McpClient {
  private readonly child: ChildProcessWithoutNullStreams;
  private readonly pending = new Map<number, (msg: JsonRpcResponse) => void>();
  private nextId = 1;
  private buffer = '';

  constructor(entry: string) {
    this.child = spawn(process.execPath, [entry], {
      stdio: ['pipe', 'pipe', 'inherit'],
      env: { ...process.env },
    });
    this.child.stdout.setEncoding('utf8');
    this.child.stdout.on('data', (chunk: string) => this.onData(chunk));
  }

  private onData(chunk: string): void {
    this.buffer += chunk;
    let idx: number;
    while ((idx = this.buffer.indexOf('\n')) !== -1) {
      const line = this.buffer.slice(0, idx).trim();
      this.buffer = this.buffer.slice(idx + 1);
      if (!line) continue;
      let msg: JsonRpcResponse;
      try {
        msg = JSON.parse(line);
      } catch {
        console.error(`  non-JSON line from server: ${line.slice(0, 120)}`);
        continue;
      }
      const cb = this.pending.get(msg.id);
      if (cb) {
        this.pending.delete(msg.id);
        cb(msg);
      }
    }
  }

  rpc<T>(method: string, params?: unknown): Promise<T> {
    const id = this.nextId++;
    return new Promise<T>((resolveP, rejectP) => {
      const timer = setTimeout(() => {
        this.pending.delete(id);
        rejectP(new Error(`timed out waiting for ${method}`));
      }, TIMEOUT_MS);
      this.pending.set(id, (msg) => {
        clearTimeout(timer);
        if (msg.error) {
          rejectP(new Error(`${method} failed: ${msg.error.message}`));
          return;
        }
        resolveP(msg.result as T);
      });
      this.child.stdin.write(`${JSON.stringify({ jsonrpc: '2.0', id, method, params })}\n`);
    });
  }

  notify(method: string): void {
    this.child.stdin.write(`${JSON.stringify({ jsonrpc: '2.0', method })}\n`);
  }

  dispose(): void {
    this.child.stdin.end();
    this.child.kill();
  }
}

function checkVersionFlag(server: ServerUnderTest, entry: string): Check {
  const pkgJson: unknown = JSON.parse(
    readFileSync(resolve(__dirname, '..', 'packages', server.pkg, 'package.json'), 'utf8'),
  );
  const version = isRecord(pkgJson) ? String(pkgJson.version) : '?';
  const expected = `${server.serverName} ${version}`;
  try {
    const output = execFileSync(process.execPath, [entry, '--version'], {
      encoding: 'utf8',
      timeout: TIMEOUT_MS,
    }).trim();
    return {
      name: `${server.serverName}: --version`,
      ok: output === expected,
      detail: output === expected ? output : `expected '${expected}', got '${output}'`,
    };
  } catch (err) {
    return {
      name: `${server.serverName}: --version`,
      ok: false,
      detail: err instanceof Error ? err.message : String(err),
    };
  }
}

async function checkServer(server: ServerUnderTest): Promise<Check[]> {
  const checks: Check[] = [];
  const entry = resolve(__dirname, '..', 'packages', server.pkg, 'dist', 'index.js');

  if (!existsSync(entry)) {
    return [
      {
        name: `${server.serverName}: dist present`,
        ok: false,
        detail: `${entry} missing — run 'pnpm build' first`,
      },
    ];
  }

  checks.push(checkVersionFlag(server, entry));

  const client = new McpClient(entry);
  try {
    const init = await client.rpc<{ serverInfo: { name: string; version: string } }>('initialize', {
      protocolVersion: '2024-11-05',
      capabilities: {},
      clientInfo: { name: 'ferrlabs-mcp-smoke', version: '1.0.0' },
    });
    checks.push({
      name: `${server.serverName}: initialize`,
      ok: init.serverInfo.name === server.serverName,
      detail: `serverInfo: ${init.serverInfo.name}@${init.serverInfo.version}`,
    });

    client.notify('notifications/initialized');

    const list = await client.rpc<{ tools: { name: string }[] }>('tools/list');
    const names = new Set(list.tools.map((t) => t.name));
    const missing = server.expectedTools.filter((n) => !names.has(n));
    checks.push({
      name: `${server.serverName}: tools/list`,
      ok: missing.length === 0,
      detail:
        missing.length === 0
          ? `${list.tools.length} tools registered`
          : `missing: ${missing.join(', ')}`,
    });

    if (server.expectedResourceTemplates) {
      const listed = await client.rpc<{ resourceTemplates: { uriTemplate: string }[] }>(
        'resources/templates/list',
      );
      const advertised = new Set(listed.resourceTemplates.map((r) => r.uriTemplate));
      const absent = server.expectedResourceTemplates.filter((t) => !advertised.has(t));
      checks.push({
        name: `${server.serverName}: resources/templates/list`,
        ok: absent.length === 0,
        detail:
          absent.length === 0
            ? `${listed.resourceTemplates.length} templates advertised`
            : `missing: ${absent.join(', ')}`,
      });
    }

    if (server.expectedPrompts) {
      const listed = await client.rpc<{ prompts: { name: string }[] }>('prompts/list');
      const advertised = new Set(listed.prompts.map((p) => p.name));
      const absent = server.expectedPrompts.filter((n) => !advertised.has(n));
      checks.push({
        name: `${server.serverName}: prompts/list`,
        ok: absent.length === 0,
        detail:
          absent.length === 0
            ? `${listed.prompts.length} prompts advertised`
            : `missing: ${absent.join(', ')}`,
      });
    }

    for (const call of server.liveCalls ?? []) {
      const result = await client.rpc<ToolResult>('tools/call', {
        name: call.tool,
        arguments: call.arguments ?? {},
      });
      const text = result.content?.[0]?.text ?? '';
      let parsed: unknown;
      try {
        parsed = JSON.parse(text);
      } catch {
        parsed = undefined;
      }
      const ok = !result.isError && call.check(parsed);
      checks.push({
        name: `${server.serverName}: tools/call ${call.tool}`,
        ok,
        detail: ok ? call.describe(parsed) : `unexpected response: ${text.slice(0, 120)}`,
      });
    }
  } catch (err) {
    checks.push({
      name: `${server.serverName}: boot`,
      ok: false,
      detail: err instanceof Error ? err.message : String(err),
    });
  } finally {
    client.dispose();
  }

  return checks;
}

async function run(): Promise<number> {
  const checks: Check[] = [];
  for (const server of SERVERS) {
    // eslint-disable-next-line no-await-in-loop -- one subprocess at a time keeps the output readable
    checks.push(...(await checkServer(server)));
  }

  let failed = 0;
  for (const c of checks) {
    console.log(`[${c.ok ? 'PASS' : 'FAIL'}] ${c.name} — ${c.detail}`);
    if (!c.ok) failed++;
  }
  console.log('');
  if (failed === 0) {
    console.log(`Smoke: ${checks.length}/${checks.length} OK across ${SERVERS.length} servers`);
    return 0;
  }
  console.error(`Smoke: ${failed}/${checks.length} FAILED`);
  return 1;
}

run()
  .then((code) => process.exit(code))
  .catch((err: unknown) => {
    console.error('Smoke aborted:', err instanceof Error ? err.message : err);
    process.exit(1);
  });
