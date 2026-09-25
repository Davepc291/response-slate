import { TestBed } from '@angular/core/testing';
import { ActivatedRoute, convertToParamMap, provideRouter, Router } from '@angular/router';
import { of } from 'rxjs';

import { AuthResult } from '../auth-data/auth-api.service';
import { AuthApiService } from '../auth-data/auth-api.service';
import { StatusResponse } from '../auth-data/auth-api.models';
import { MFAVerifyBeginResponse } from '../auth-data/webauthn.models';
import { MobileAuthMfaVerify } from './mobile-auth-mfa-verify';

const BEGIN_RESPONSE: MFAVerifyBeginResponse = {
  publicKey: {
    challenge: 'Y2hhbGxlbmdl',
    allowCredentials: [{ type: 'public-key', id: 'Y3JlZC1pZA' }],
  },
};

function fakeCredential(): PublicKeyCredential {
  return {
    id: 'Y3JlZC1pZA',
    rawId: new TextEncoder().encode('cred-id').buffer,
    type: 'public-key',
    response: {
      clientDataJSON: new TextEncoder().encode('client-data').buffer,
      authenticatorData: new TextEncoder().encode('auth-data').buffer,
      signature: new TextEncoder().encode('sig').buffer,
      userHandle: null,
    },
    getClientExtensionResults: () => ({}),
  } as unknown as PublicKeyCredential;
}

describe('MobileAuthMfaVerify', () => {
  let mfaVerifyBegin: ReturnType<
    typeof vi.fn<() => ReturnType<AuthApiService['mfaVerifyBegin']>>
  >;
  let mfaVerifyFinish: ReturnType<
    typeof vi.fn<(credential: unknown) => ReturnType<AuthApiService['mfaVerifyFinish']>>
  >;
  let navigateByUrl: ReturnType<typeof vi.fn>;
  let get: ReturnType<typeof vi.fn>;
  let originalPublicKeyCredential: unknown;
  let originalCredentials: CredentialsContainer;

  beforeEach(() => {
    originalPublicKeyCredential = (window as { PublicKeyCredential?: unknown })
      .PublicKeyCredential;
    originalCredentials = navigator.credentials;
    (window as { PublicKeyCredential?: unknown }).PublicKeyCredential = function () {
      /* stub constructor */
    };
    get = vi.fn();
    Object.defineProperty(navigator, 'credentials', {
      value: { create: vi.fn(), get },
      configurable: true,
    });
  });

  afterEach(() => {
    (window as { PublicKeyCredential?: unknown }).PublicKeyCredential =
      originalPublicKeyCredential;
    Object.defineProperty(navigator, 'credentials', {
      value: originalCredentials,
      configurable: true,
    });
  });

  function configure(returnUrl: string | null = null) {
    mfaVerifyBegin = vi.fn();
    mfaVerifyFinish = vi.fn();
    TestBed.configureTestingModule({
      imports: [MobileAuthMfaVerify],
      providers: [
        provideRouter([]),
        { provide: AuthApiService, useValue: { mfaVerifyBegin, mfaVerifyFinish } },
        {
          provide: ActivatedRoute,
          useValue: {
            snapshot: { queryParamMap: convertToParamMap(returnUrl ? { returnUrl } : {}) },
          },
        },
      ],
    });
    navigateByUrl = vi
      .spyOn(TestBed.inject(Router), 'navigateByUrl')
      .mockResolvedValue(true) as never;
    const fixture = TestBed.createComponent(MobileAuthMfaVerify);
    fixture.detectChanges();
    return fixture;
  }

  function clickVerify(fixture: ReturnType<typeof configure>) {
    const el = fixture.nativeElement as HTMLElement;
    el.querySelector<HTMLButtonElement>('button.primary')?.click();
    fixture.detectChanges();
  }

  async function flush(fixture: ReturnType<typeof configure>) {
    await Promise.resolve();
    await Promise.resolve();
    await Promise.resolve();
    fixture.detectChanges();
  }

  it('shows the unsupported-browser message and no action button when WebAuthn is unavailable', () => {
    delete (window as { PublicKeyCredential?: unknown }).PublicKeyCredential;
    const fixture = configure();
    const el = fixture.nativeElement as HTMLElement;
    expect(el.textContent).toContain('Passkeys are not supported');
    expect(el.querySelector('button.primary')).toBeNull();
    fixture.destroy();
  });

  it('explains that admin access requires passkey verification', () => {
    const fixture = configure();
    const el = fixture.nativeElement as HTMLElement;
    expect(el.textContent).toContain('Administrator access requires passkey verification');
    fixture.destroy();
  });

  it('begins verification, calls navigator.credentials.get with decoded ArrayBuffer fields, and sends the base64url-encoded assertion to finish', async () => {
    const fixture = configure();
    mfaVerifyBegin.mockReturnValue(
      of<AuthResult<MFAVerifyBeginResponse>>({ ok: true, value: BEGIN_RESPONSE }),
    );
    get.mockResolvedValue(fakeCredential());
    mfaVerifyFinish.mockReturnValue(
      of<AuthResult<StatusResponse>>({ ok: true, value: { status: 'mfa_verified' } }),
    );

    clickVerify(fixture);
    expect(mfaVerifyBegin).toHaveBeenCalledOnce();
    await flush(fixture);

    expect(get).toHaveBeenCalledOnce();
    const options = get.mock.calls[0][0] as CredentialRequestOptions;
    expect(options.publicKey?.challenge).toBeInstanceOf(Uint8Array);
    expect(options.publicKey?.allowCredentials?.[0].id).toBeInstanceOf(Uint8Array);

    expect(mfaVerifyFinish).toHaveBeenCalledOnce();
    const payload = mfaVerifyFinish.mock.calls[0][0] as {
      id: string;
      response: { authenticatorData: string; signature: string };
    };
    expect(payload.id).toBe('Y3JlZC1pZA');
    expect(typeof payload.response.authenticatorData).toBe('string');
    expect(typeof payload.response.signature).toBe('string');
    fixture.destroy();
  });

  it('re-enables the action and shows a safe message when the browser ceremony is cancelled', async () => {
    const fixture = configure();
    mfaVerifyBegin.mockReturnValue(
      of<AuthResult<MFAVerifyBeginResponse>>({ ok: true, value: BEGIN_RESPONSE }),
    );
    get.mockRejectedValue(new DOMException('cancelled', 'NotAllowedError'));

    clickVerify(fixture);
    await flush(fixture);

    const el = fixture.nativeElement as HTMLElement;
    expect(el.textContent).toContain('cancelled or could not be completed');
    expect(mfaVerifyFinish).not.toHaveBeenCalled();
    expect(el.querySelector<HTMLButtonElement>('button.primary')?.disabled).toBe(false);
    fixture.destroy();
  });

  it('shows a safe message and does not call finish when the begin call itself fails', async () => {
    const fixture = configure();
    mfaVerifyBegin.mockReturnValue(
      of<AuthResult<MFAVerifyBeginResponse>>({
        ok: false,
        error: { kind: 'mfa_not_eligible', message: 'This account cannot complete this passkey step right now.' },
      }),
    );

    clickVerify(fixture);
    await flush(fixture);

    const el = fixture.nativeElement as HTMLElement;
    expect(el.textContent).toContain('This account cannot complete this passkey step right now.');
    expect(get).not.toHaveBeenCalled();
    fixture.destroy();
  });

  it('returns to the default admin area on successful verification when no returnUrl was given', async () => {
    const fixture = configure();
    mfaVerifyBegin.mockReturnValue(
      of<AuthResult<MFAVerifyBeginResponse>>({ ok: true, value: BEGIN_RESPONSE }),
    );
    get.mockResolvedValue(fakeCredential());
    mfaVerifyFinish.mockReturnValue(
      of<AuthResult<StatusResponse>>({ ok: true, value: { status: 'mfa_verified' } }),
    );

    clickVerify(fixture);
    await flush(fixture);

    expect(navigateByUrl).toHaveBeenCalledExactlyOnceWith('/mobile/auth/admin/users');
    fixture.destroy();
  });

  it('returns to the requested admin returnUrl on successful verification', async () => {
    const fixture = configure('/mobile/auth/admin/users/42');
    mfaVerifyBegin.mockReturnValue(
      of<AuthResult<MFAVerifyBeginResponse>>({ ok: true, value: BEGIN_RESPONSE }),
    );
    get.mockResolvedValue(fakeCredential());
    mfaVerifyFinish.mockReturnValue(
      of<AuthResult<StatusResponse>>({ ok: true, value: { status: 'mfa_verified' } }),
    );

    clickVerify(fixture);
    await flush(fixture);

    expect(navigateByUrl).toHaveBeenCalledExactlyOnceWith('/mobile/auth/admin/users/42');
    fixture.destroy();
  });

  it('ignores a returnUrl outside /mobile/auth/admin/ (open-redirect guard)', async () => {
    const fixture = configure('https://evil.example.com/');
    mfaVerifyBegin.mockReturnValue(
      of<AuthResult<MFAVerifyBeginResponse>>({ ok: true, value: BEGIN_RESPONSE }),
    );
    get.mockResolvedValue(fakeCredential());
    mfaVerifyFinish.mockReturnValue(
      of<AuthResult<StatusResponse>>({ ok: true, value: { status: 'mfa_verified' } }),
    );

    clickVerify(fixture);
    await flush(fixture);

    expect(navigateByUrl).toHaveBeenCalledExactlyOnceWith('/mobile/auth/admin/users');
    fixture.destroy();
  });

  it('writes no browser storage during a full verification cycle', async () => {
    const fixture = configure();
    mfaVerifyBegin.mockReturnValue(
      of<AuthResult<MFAVerifyBeginResponse>>({ ok: true, value: BEGIN_RESPONSE }),
    );
    get.mockResolvedValue(fakeCredential());
    mfaVerifyFinish.mockReturnValue(
      of<AuthResult<StatusResponse>>({ ok: true, value: { status: 'mfa_verified' } }),
    );
    const storageWrite = vi.spyOn(Storage.prototype, 'setItem');

    clickVerify(fixture);
    await flush(fixture);

    expect(storageWrite).not.toHaveBeenCalled();
    storageWrite.mockRestore();
    fixture.destroy();
  });
});
