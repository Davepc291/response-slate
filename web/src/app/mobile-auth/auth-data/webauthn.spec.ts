import {
  assertionCredentialToJSON,
  base64UrlToBytes,
  bytesToBase64Url,
  isPublicKeyCredential,
  isValidCreationOptions,
  isValidRequestOptions,
  isWebAuthnSupported,
  registrationCredentialToJSON,
  toCreationOptions,
  toRequestOptions,
} from './webauthn';

describe('webauthn base64url conversion', () => {
  it('round-trips arbitrary bytes through bytesToBase64Url/base64UrlToBytes', () => {
    const original = new Uint8Array([0, 1, 2, 3, 250, 251, 252, 253, 254, 255, 16, 32, 64, 128]);
    const encoded = bytesToBase64Url(original);
    expect(encoded).not.toContain('+');
    expect(encoded).not.toContain('/');
    expect(encoded).not.toContain('=');
    expect(Array.from(base64UrlToBytes(encoded))).toEqual(Array.from(original));
  });

  it('decodes exactly like go base64.RawURLEncoding for a known vector', () => {
    // "hello world" -> base64url (no padding), independently verifiable.
    const bytes = base64UrlToBytes('aGVsbG8gd29ybGQ');
    const text = new TextDecoder().decode(bytes);
    expect(text).toBe('hello world');
  });

  it('encodes bytes whose length requires padding without emitting any "=" characters', () => {
    const encoded = bytesToBase64Url(new Uint8Array([1]));
    expect(encoded).not.toContain('=');
    expect(base64UrlToBytes(encoded)).toEqual(new Uint8Array([1]));
  });

  it('round-trips an ArrayBuffer input, not only a Uint8Array', () => {
    const buffer = new Uint8Array([9, 8, 7]).buffer;
    const encoded = bytesToBase64Url(buffer);
    expect(Array.from(base64UrlToBytes(encoded))).toEqual([9, 8, 7]);
  });
});

describe('isWebAuthnSupported', () => {
  const originalPublicKeyCredential = (window as { PublicKeyCredential?: unknown })
    .PublicKeyCredential;
  const originalCredentials = navigator.credentials;

  afterEach(() => {
    if (originalPublicKeyCredential === undefined) {
      delete (window as { PublicKeyCredential?: unknown }).PublicKeyCredential;
    } else {
      (window as { PublicKeyCredential?: unknown }).PublicKeyCredential =
        originalPublicKeyCredential;
    }
    Object.defineProperty(navigator, 'credentials', {
      value: originalCredentials,
      configurable: true,
    });
  });

  it('is false when window.PublicKeyCredential is unavailable', () => {
    delete (window as { PublicKeyCredential?: unknown }).PublicKeyCredential;
    expect(isWebAuthnSupported()).toBe(false);
  });

  it('is false when navigator.credentials is unavailable, even with PublicKeyCredential present', () => {
    (window as { PublicKeyCredential?: unknown }).PublicKeyCredential = function () {
      /* stub constructor */
    };
    Object.defineProperty(navigator, 'credentials', { value: undefined, configurable: true });
    expect(isWebAuthnSupported()).toBe(false);
  });

  it('is true when both PublicKeyCredential and navigator.credentials exist', () => {
    (window as { PublicKeyCredential?: unknown }).PublicKeyCredential = function () {
      /* stub constructor */
    };
    Object.defineProperty(navigator, 'credentials', {
      value: { create: vi.fn(), get: vi.fn() },
      configurable: true,
    });
    expect(isWebAuthnSupported()).toBe(true);
  });
});

describe('isValidCreationOptions / isValidRequestOptions', () => {
  it('accepts a well-formed begin-enrollment response', () => {
    expect(
      isValidCreationOptions({
        publicKey: {
          rp: { name: 'Greenwich Fire Responder' },
          user: { id: 'AAA', name: 'a@example.com', displayName: 'A' },
          challenge: 'Y2hhbGxlbmdl',
          pubKeyCredParams: [{ type: 'public-key', alg: -7 }],
        },
      }),
    ).toBe(true);
  });

  it('rejects a response missing challenge/user/rp', () => {
    expect(isValidCreationOptions(null)).toBe(false);
    expect(isValidCreationOptions({ publicKey: {} as never })).toBe(false);
  });

  it('accepts a well-formed begin-verification response', () => {
    expect(isValidRequestOptions({ publicKey: { challenge: 'Y2hhbGxlbmdl' } })).toBe(true);
  });

  it('rejects a response missing challenge', () => {
    expect(isValidRequestOptions({ publicKey: {} as never })).toBe(false);
    expect(isValidRequestOptions(undefined)).toBe(false);
  });
});

describe('toCreationOptions / toRequestOptions', () => {
  it('decodes the challenge, user id, and excludeCredentials ids to ArrayBuffer-backed views', () => {
    const options = toCreationOptions({
      publicKey: {
        rp: { name: 'Greenwich Fire Responder' },
        user: { id: 'dXNlci1pZA', name: 'a@example.com', displayName: 'A' },
        challenge: 'Y2hhbGxlbmdl',
        pubKeyCredParams: [{ type: 'public-key', alg: -7 }],
        excludeCredentials: [{ type: 'public-key', id: 'Y3JlZC1pZA' }],
      },
    });
    const publicKey = options.publicKey!;
    expect(publicKey.challenge).toBeInstanceOf(Uint8Array);
    expect(new TextDecoder().decode(publicKey.challenge as Uint8Array)).toBe('challenge');
    expect(new TextDecoder().decode(publicKey.user.id as Uint8Array)).toBe('user-id');
    expect(
      new TextDecoder().decode(publicKey.excludeCredentials![0].id as Uint8Array),
    ).toBe('cred-id');
  });

  it('decodes the challenge and allowCredentials ids for a request', () => {
    const options = toRequestOptions({
      publicKey: {
        challenge: 'Y2hhbGxlbmdl',
        allowCredentials: [{ type: 'public-key', id: 'Y3JlZC1pZA' }],
      },
    });
    const publicKey = options.publicKey!;
    expect(new TextDecoder().decode(publicKey.challenge as Uint8Array)).toBe('challenge');
    expect(
      new TextDecoder().decode(publicKey.allowCredentials![0].id as Uint8Array),
    ).toBe('cred-id');
  });
});

function fakePublicKeyCredential(
  response: Partial<AuthenticatorAttestationResponse & AuthenticatorAssertionResponse>,
): PublicKeyCredential {
  return {
    id: 'Y3JlZC1pZA',
    rawId: new TextEncoder().encode('cred-id').buffer,
    type: 'public-key',
    response,
    getClientExtensionResults: () => ({}),
  } as unknown as PublicKeyCredential;
}

describe('isPublicKeyCredential', () => {
  it('rejects null and a plain Credential lacking rawId/response', () => {
    expect(isPublicKeyCredential(null)).toBe(false);
    expect(isPublicKeyCredential({ id: 'x', type: 'public-key' } as Credential)).toBe(false);
  });

  it('accepts a value with rawId and response', () => {
    expect(isPublicKeyCredential(fakePublicKeyCredential({}))).toBe(true);
  });
});

describe('registrationCredentialToJSON / assertionCredentialToJSON', () => {
  it('base64url-encodes every binary field of a registration response', () => {
    const credential = fakePublicKeyCredential({
      clientDataJSON: new TextEncoder().encode('client-data').buffer,
      attestationObject: new TextEncoder().encode('attestation').buffer,
    });
    const json = registrationCredentialToJSON(credential);
    expect(json.id).toBe('Y3JlZC1pZA');
    expect(new TextDecoder().decode(base64UrlToBytes(json.rawId))).toBe('cred-id');
    expect(new TextDecoder().decode(base64UrlToBytes(json.response.clientDataJSON))).toBe(
      'client-data',
    );
    expect(new TextDecoder().decode(base64UrlToBytes(json.response.attestationObject))).toBe(
      'attestation',
    );
    expect(json.clientExtensionResults).toEqual({});
  });

  it('base64url-encodes every binary field of an assertion response, including userHandle', () => {
    const credential = fakePublicKeyCredential({
      clientDataJSON: new TextEncoder().encode('client-data').buffer,
      authenticatorData: new TextEncoder().encode('auth-data').buffer,
      signature: new TextEncoder().encode('sig').buffer,
      userHandle: new TextEncoder().encode('user-handle').buffer,
    });
    const json = assertionCredentialToJSON(credential);
    expect(new TextDecoder().decode(base64UrlToBytes(json.response.authenticatorData))).toBe(
      'auth-data',
    );
    expect(new TextDecoder().decode(base64UrlToBytes(json.response.signature))).toBe('sig');
    expect(new TextDecoder().decode(base64UrlToBytes(json.response.userHandle!))).toBe(
      'user-handle',
    );
  });

  it('omits userHandle when the authenticator did not supply one', () => {
    const credential = fakePublicKeyCredential({
      clientDataJSON: new TextEncoder().encode('client-data').buffer,
      authenticatorData: new TextEncoder().encode('auth-data').buffer,
      signature: new TextEncoder().encode('sig').buffer,
      userHandle: null as unknown as ArrayBuffer,
    });
    const json = assertionCredentialToJSON(credential);
    expect(json.response.userHandle).toBeUndefined();
  });
});
