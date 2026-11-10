#!/usr/bin/env node
import { runMcp, readPackageVersion } from '@ferrlabs/mcp-core';
import { register } from './register.js';

try {
  await runMcp({
    name: 'ferrvault',
    version: readPackageVersion(import.meta.url),
    scopes: ['secrets:read', 'secrets:write'],
    register,
  });
} catch (err: unknown) {
  console.error('ferrvault-mcp fatal:', err instanceof Error ? err.message : err);
  process.exit(1);
}
