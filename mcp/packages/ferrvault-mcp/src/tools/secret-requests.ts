import { getToken, type McpServer } from '@ferrlabs/mcp-core';
import {
  type SecretTarget,
  archiveSecretRequestPath,
  secretRequestsPath,
  vaultRequest,
} from '../api.js';
import { guarded, textResult } from '../results.js';
import { environmentSlug, requestedName, secretRequestId, vaultSlug } from '../schemas.js';

interface SecretRequest {
  id: string;
  name: string;
  state: 'pending' | 'fulfilled' | 'archived';
  request_count: number;
  first_requested_at: string;
  last_requested_at: string;
  fulfilled_at: string | null;
  last_requester_kind: string;
}

async function listRequests(token: string, target: SecretTarget): Promise<SecretRequest[]> {
  const { requests } = await vaultRequest<{ requests: SecretRequest[] }>(
    secretRequestsPath(target),
    { token },
  );
  return requests;
}

function summary(request: SecretRequest): SecretRequest {
  const {
    id,
    name,
    state,
    request_count,
    first_requested_at,
    last_requested_at,
    fulfilled_at,
    last_requester_kind,
  } = request;
  return {
    id,
    name,
    state,
    request_count,
    first_requested_at,
    last_requested_at,
    fulfilled_at,
    last_requester_kind,
  };
}

type RequestRef = { id: string } | { name: string };

function requestRef(id: string | undefined, name: string | undefined): RequestRef {
  if (id !== undefined && name === undefined) return { id };
  if (name !== undefined && id === undefined) return { name };
  throw new Error('pass exactly one of id or name');
}

async function resolvePendingId(
  token: string,
  target: SecretTarget,
  name: string,
): Promise<string> {
  const requests = await listRequests(token, target);
  const pending = requests.find((r) => r.name === name && r.state === 'pending');
  if (!pending) {
    throw new Error(
      `no pending secret request named ${name} in ${target.vault}/${target.environment}`,
    );
  }
  return pending.id;
}

export function registerSecretRequestTools(server: McpServer): void {
  server.tool(
    'list_ferrvault_secret_requests',
    'List the pending secret requests of one vault environment: names a client (operator, CLI) asked for that have no value yet, with request counts, timestamps and requester kind. The API returns pending requests only; fulfilled and archived ones are not listed.',
    { vault: vaultSlug, environment: environmentSlug },
    ({ vault, environment }) =>
      guarded(async () => {
        const token = await getToken();
        return textResult((await listRequests(token, { vault, environment })).map(summary));
      }),
  );

  server.tool(
    'archive_ferrvault_secret_request',
    'Archive a pending secret request (a false positive, e.g. a typo), by id or by secret name. An archived request stays archived even if clients keep asking for the name. Pass exactly one of id or name.',
    {
      vault: vaultSlug,
      environment: environmentSlug,
      id: secretRequestId.optional(),
      name: requestedName.optional(),
    },
    ({ vault, environment, id, name }) =>
      guarded(async () => {
        const ref = requestRef(id, name);
        const token = await getToken();
        const target = { vault, environment };
        const requestId = 'id' in ref ? ref.id : await resolvePendingId(token, target, ref.name);
        await vaultRequest<void>(archiveSecretRequestPath(target, requestId), {
          token,
          method: 'POST',
        });
        return textResult({ archived: requestId, name, vault, environment, state: 'archived' });
      }),
  );
}
