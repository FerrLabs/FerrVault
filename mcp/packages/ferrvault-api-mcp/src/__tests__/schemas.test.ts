import { describe, it, expect } from 'vitest';
import { environmentSlug, secretName, secretRequestId, vaultSlug } from '../schemas.js';
import { CHARSETS, generateValue } from '../generate.js';

describe('path parameter schemas', () => {
  it.each(['..', '.', 'a/b', 'prod%2F..', 'Prod', ''])('rejects %j as a slug', (input) => {
    expect(vaultSlug.safeParse(input).success).toBe(false);
    expect(environmentSlug.safeParse(input).success).toBe(false);
  });

  it('accepts the slugs FerrVault creates', () => {
    expect(vaultSlug.safeParse('ferrlabs-infra').success).toBe(true);
    expect(environmentSlug.safeParse('prod').success).toBe(true);
  });

  it.each(['..', 'a/b', '1ABC', 'KEY?x=1'])('rejects %j as a secret name', (input) => {
    expect(secretName.safeParse(input).success).toBe(false);
  });

  it('accepts dotted and underscored secret names', () => {
    expect(secretName.safeParse('_tls.crt').success).toBe(true);
  });

  it.each(['..', '../archive', '0b0e3c4e-6c2f-4f0e-9a51-7a1f2d1c9e01/..', 'not-a-uuid'])(
    'rejects %j as a secret request id',
    (input) => {
      expect(secretRequestId.safeParse(input).success).toBe(false);
    },
  );

  it('accepts a secret request uuid', () => {
    expect(secretRequestId.safeParse('0b0e3c4e-6c2f-4f0e-9a51-7a1f2d1c9e01').success).toBe(true);
  });
});

describe('generateValue', () => {
  it('only draws characters from the requested charset', () => {
    for (const [name, alphabet] of Object.entries(CHARSETS)) {
      const value = generateValue(2048, name as keyof typeof CHARSETS);
      expect(value).toHaveLength(2048);
      expect([...value].every((c) => alphabet.includes(c))).toBe(true);
    }
  });

  it('never emits quotes, backslashes or whitespace in the ascii charset', () => {
    expect(generateValue(4096, 'ascii')).not.toMatch(/["'`\\\s]/);
  });
});
