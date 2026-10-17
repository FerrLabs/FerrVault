import { randomInt } from 'node:crypto';

const LOWER = 'abcdefghijklmnopqrstuvwxyz';
const UPPER = LOWER.toUpperCase();
const DIGITS = '0123456789';

export const CHARSETS = {
  alphanumeric: LOWER + UPPER + DIGITS,
  hex: DIGITS + 'abcdef',
  base64url: LOWER + UPPER + DIGITS + '-_',
  ascii: LOWER + UPPER + DIGITS + '!#$%&()*+,-./:;<=>?@[]^_{|}~',
} as const;

export type Charset = keyof typeof CHARSETS;

export function generateValue(length: number, charset: Charset): string {
  const alphabet = CHARSETS[charset];
  return Array.from({ length }, () => alphabet[randomInt(alphabet.length)]).join('');
}
