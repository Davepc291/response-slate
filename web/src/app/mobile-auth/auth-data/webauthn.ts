import {
  MFAEnrollBeginResponse,
  MFAVerifyBeginResponse,
  WebAuthnAuthenticationResponseJSON,
  WebAuthnRegistrationResponseJSON,
} from './webauthn.models';

/**
 * Minimal browser WebAuthn helper for Step 9F-5. Every function here is a
 * pure conversion or a thin wrapper around the standard
 * `navigator.credentials` API — no ceremony state (challenge, attestation,
 * credential id, assertion) is ever written to localStorage,
 * sessionStorage, IndexedDB, or any app-persistent signal; each value lives
 * only in the Promise/Observable chain that produced it.
 */

/**
 * Feature detection (Step 9F-5 requirement 9): never assume WebAuthn is
 * available. A caller must check this before offering enrollment/
 * verification and must never silently fall back to TOTP or SMS when it is
 * false — this task implements neither.
 */
export function isWebAuthnSupported(): boolean {
  return (
    typeof window !== 'undefined' &&
    typeof window.PublicKeyCredential !== 'undefined' &&
    typeof navigator !== 'undefined' &&
    !!navigator.credentials
  );
}

/** Decodes a base64url (no padding) string, matching go's base64.RawURLEncoding. */
export function base64UrlToBytes(value: string): Uint8Array {
  const normalized = value.replace(/-/g, '+').replace(/_/g, '/');
  const padLength = (4 - (normalized.length % 4)) % 4;
  const padded = normalized + '='.repeat(padLength);
  const binary = atob(padded);
  const bytes = new Uint8Array(binary.length);
  for (let i = 0; i < binary.length; i += 1) {
    bytes[i] = binary.charCodeAt(i);
  }
  return bytes;
}

/** Encodes to base64url (no padding), matching go's base64.RawURLEncoding. */
export function bytesToBase64Url(source: ArrayBuffer | Uint8Array): string {
  const bytes = source instanceof Uint8Array ? source : new Uint8Array(source);
  let binary = '';
  for (let i = 0; i < bytes.length; i += 1) {
    binary += String.fromCharCode(bytes[i]);
  }
  return btoa(binary).replace(/\+/g, '-').replace(/\//g, '_').replace(/=+$/, '');
}

/**
 * Defensive runtime check that a "begin enrollment" response actually has
 * the shape navigator.credentials.create() needs, rather than trusting the
 * declared TypeScript type of an HTTP response body. Never itself a
 * security boundary (the backend's ceremony verification is), just a guard
 * against calling create() with a malformed options object.
 */
export function isValidCreationOptions(
  value: MFAEnrollBeginResponse | null | undefined,
): value is MFAEnrollBeginResponse {
  const pk = value?.publicKey;
  return (
    !!pk &&
    typeof pk.challenge === 'string' &&
    pk.challenge.length > 0 &&
    !!pk.user &&
    typeof pk.user.id === 'string' &&
    !!pk.rp
  );
}

/** Same defensive check as {@link isValidCreationOptions}, for verification. */
export function isValidRequestOptions(
  value: MFAVerifyBeginResponse | null | undefined,
): value is MFAVerifyBeginResponse {
  const pk = value?.publicKey;
  return !!pk && typeof pk.challenge === 'string' && pk.challenge.length > 0;
}

/** Converts a begin-enrollment response into navigator.credentials.create()'s argument. */
export function toCreationOptions(response: MFAEnrollBeginResponse): CredentialCreationOptions {
  const pk = response.publicKey;
  return {
    publicKey: {
      ...pk,
      challenge: base64UrlToBytes(pk.challenge),
      user: { ...pk.user, id: base64UrlToBytes(pk.user.id) },
      excludeCredentials: pk.excludeCredentials?.map((cred) => ({
        ...cred,
        id: base64UrlToBytes(cred.id),
      })),
    } as PublicKeyCredentialCreationOptions,
  };
}

/** Converts a begin-verification response into navigator.credentials.get()'s argument. */
export function toRequestOptions(response: MFAVerifyBeginResponse): CredentialRequestOptions {
  const pk = response.publicKey;
  return {
    publicKey: {
      ...pk,
      challenge: base64UrlToBytes(pk.challenge),
      allowCredentials: pk.allowCredentials?.map((cred) => ({
        ...cred,
        id: base64UrlToBytes(cred.id),
      })),
    } as PublicKeyCredentialRequestOptions,
  };
}

/** Narrows navigator.credentials.create()/get()'s `Credential | null` result. */
export function isPublicKeyCredential(value: Credential | null): value is PublicKeyCredential {
  return !!value && 'rawId' in value && 'response' in value;
}

/** Converts a create() result into the JSON body FinishEnrollment expects. */
export function registrationCredentialToJSON(
  credential: PublicKeyCredential,
): WebAuthnRegistrationResponseJSON {
  const response = credential.response as AuthenticatorAttestationResponse;
  return {
    id: credential.id,
    rawId: bytesToBase64Url(credential.rawId),
    type: credential.type,
    response: {
      clientDataJSON: bytesToBase64Url(response.clientDataJSON),
      attestationObject: bytesToBase64Url(response.attestationObject),
    },
    clientExtensionResults: credential.getClientExtensionResults(),
  };
}

/** Converts a get() result into the JSON body FinishLogin expects. */
export function assertionCredentialToJSON(
  credential: PublicKeyCredential,
): WebAuthnAuthenticationResponseJSON {
  const response = credential.response as AuthenticatorAssertionResponse;
  return {
    id: credential.id,
    rawId: bytesToBase64Url(credential.rawId),
    type: credential.type,
    response: {
      clientDataJSON: bytesToBase64Url(response.clientDataJSON),
      authenticatorData: bytesToBase64Url(response.authenticatorData),
      signature: bytesToBase64Url(response.signature),
      userHandle: response.userHandle ? bytesToBase64Url(response.userHandle) : undefined,
    },
    clientExtensionResults: credential.getClientExtensionResults(),
  };
}
