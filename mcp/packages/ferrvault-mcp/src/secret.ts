export interface SecretMetadata {
  id: string;
  name: string;
  current_version: number;
  tags: string[];
  expires_at: string | null;
  expiry_action: 'notify' | 'disable';
  expired: boolean;
  created_at: string;
  updated_at: string;
}

export interface RevealedSecret extends SecretMetadata {
  value: string;
}

export function withoutValue(secret: SecretMetadata & { value?: unknown }): SecretMetadata {
  const { value: _value, ...metadata } = secret;
  return metadata;
}
