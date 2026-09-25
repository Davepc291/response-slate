// Minimal WebAuthn wire-format types matching backend/internal/mfa's
// go-webauthn responses exactly (protocol.CredentialCreation for
// POST /api/auth/mfa/enroll {"action":"begin"}, protocol.CredentialAssertion
// for POST /api/auth/mfa/verify {"action":"begin"}). Every byte-valued field
// is base64url text (no padding — see webauthn.ts's encode/decode helpers)
// on the wire; it is converted to/from ArrayBuffer only in webauthn.ts,
// immediately before/after the navigator.credentials call, and never stored
// anywhere in between.

export interface WebAuthnRelyingParty {
  id?: string;
  name: string;
}

export interface WebAuthnUser {
  id: string;
  name: string;
  displayName: string;
}

export interface WebAuthnCredentialParameter {
  type: string;
  alg: number;
}

export interface WebAuthnCredentialDescriptorJSON {
  type: string;
  id: string;
  transports?: string[];
}

export interface WebAuthnCreationOptionsJSON {
  rp: WebAuthnRelyingParty;
  user: WebAuthnUser;
  challenge: string;
  pubKeyCredParams: WebAuthnCredentialParameter[];
  timeout?: number;
  excludeCredentials?: WebAuthnCredentialDescriptorJSON[];
  authenticatorSelection?: Record<string, unknown>;
  attestation?: string;
  extensions?: Record<string, unknown>;
}

export interface WebAuthnRequestOptionsJSON {
  challenge: string;
  timeout?: number;
  rpId?: string;
  allowCredentials?: WebAuthnCredentialDescriptorJSON[];
  userVerification?: string;
  extensions?: Record<string, unknown>;
}

export interface MFAEnrollBeginResponse {
  publicKey: WebAuthnCreationOptionsJSON;
}

export interface MFAVerifyBeginResponse {
  publicKey: WebAuthnRequestOptionsJSON;
}

export interface WebAuthnRegistrationResponseJSON {
  id: string;
  rawId: string;
  type: string;
  response: {
    clientDataJSON: string;
    attestationObject: string;
  };
  // Typed as the DOM lib's own AuthenticationExtensionsClientOutputs (not a
  // generic Record) since that is exactly what
  // PublicKeyCredential.getClientExtensionResults() returns — it has no
  // index signature.
  clientExtensionResults: AuthenticationExtensionsClientOutputs;
  authenticatorAttachment?: string;
}

export interface WebAuthnAuthenticationResponseJSON {
  id: string;
  rawId: string;
  type: string;
  response: {
    clientDataJSON: string;
    authenticatorData: string;
    signature: string;
    userHandle?: string;
  };
  clientExtensionResults: AuthenticationExtensionsClientOutputs;
  authenticatorAttachment?: string;
}
