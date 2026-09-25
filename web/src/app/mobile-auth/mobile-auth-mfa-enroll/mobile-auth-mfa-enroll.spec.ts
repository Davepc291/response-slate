import { TestBed } from '@angular/core/testing';
import { provideRouter, Router } from '@angular/router';
import { of } from 'rxjs';

import { AuthResult } from '../auth-data/auth-api.service';
import { AuthApiService } from '../auth-data/auth-api.service';
import { MeResponse, StatusResponse } from '../auth-data/auth-api.models';
import { MFAEnrollBeginResponse } from '../auth-data/webauthn.models';
import { MobileAuthMfaEnroll } from './mobile-auth-mfa-enroll';

const NOT_AUTHENTICATED: AuthResult<MeResponse> = {
  ok: false,
  error: { kind: 'not_authenticated', message: 'Sign-in required.' },
};

const ACTIVE_ACCOUNT: MeResponse = {
  user_id: 1,
  email: 'admin@example.com',
  display_name: 'Admin',
  role: 'system_administrator',
  status: 'active',
};

const BEGIN_RESPONSE: MFAEnrollBeginResponse = {
  publicKey: {
    rp: { name: 'Greenwich Fire Responder' },
    user: { id: 'dXNlci1pZA', name: 'admin@example.com', displayName: 'Admin' },
    challenge: 'Y2hhbGxlbmdl',
    pubKeyCredParams: [{ type: 'public-key', alg: -7 }],
  },
};

function fakeCredential(): PublicKeyCredential {
  return {
    id: 'Y3JlZC1pZA',
    rawId: new TextEncoder().encode('cred-id').buffer,
    type: 'public-key',
    response: {
      clientDataJSON: new TextEncoder().encode('client-data').buffer,
      attestationObject: new TextEncoder().encode('attestation').buffer,
    },
    getClientExtensionResults: () => ({}),
  } as unknown as PublicKeyCredential;
}

describe('MobileAuthMfaEnroll', () => {
  let me: ReturnType<typeof vi.fn<() => ReturnType<AuthApiService['me']>>>;
  let mfaEnrollBegin: ReturnType<
    typeof vi.fn<() => ReturnType<AuthApiService['mfaEnrollBegin']>>
  >;
  let mfaEnrollFinish: ReturnType<
    typeof vi.fn<(credential: unknown) => ReturnType<AuthApiService['mfaEnrollFinish']>>
  >;
  let navigateByUrl: ReturnType<typeof vi.fn>;
  let create: ReturnType<typeof vi.fn>;
  let originalPublicKeyCredential: unknown;
  let originalCredentials: CredentialsContainer;

  beforeEach(() => {
    originalPublicKeyCredential = (window as { PublicKeyCredential?: unknown })
      .PublicKeyCredential;
    originalCredentials = navigator.credentials;
    (window as { PublicKeyCredential?: unknown }).PublicKeyCredential = function () {
      /* stub constructor */
    };
    create = vi.fn();
    Object.defineProperty(navigator, 'credentials', {
      value: { create, get: vi.fn() },
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

  function configure(meResult: AuthResult<MeResponse> = NOT_AUTHENTICATED) {
    me = vi.fn().mockReturnValue(of(meResult));
    mfaEnrollBegin = vi.fn();
    mfaEnrollFinish = vi.fn();
    TestBed.configureTestingModule({
      imports: [MobileAuthMfaEnroll],
      providers: [
        provideRouter([]),
        { provide: AuthApiService, useValue: { me, mfaEnrollBegin, mfaEnrollFinish } },
      ],
    });
    navigateByUrl = vi
      .spyOn(TestBed.inject(Router), 'navigateByUrl')
      .mockResolvedValue(true) as never;
    const fixture = TestBed.createComponent(MobileAuthMfaEnroll);
    fixture.detectChanges();
    return fixture;
  }

  function clickSetup(fixture: ReturnType<typeof configure>) {
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

  it('explains that administrator accounts require a passkey', () => {
    const fixture = configure();
    const el = fixture.nativeElement as HTMLElement;
    expect(el.textContent).toContain('Administrator accounts require a passkey');
    fixture.destroy();
  });

  it('begins enrollment, calls navigator.credentials.create with decoded ArrayBuffer fields, and sends the base64url-encoded result to finish', async () => {
    const fixture = configure();
    mfaEnrollBegin.mockReturnValue(
      of<AuthResult<MFAEnrollBeginResponse>>({ ok: true, value: BEGIN_RESPONSE }),
    );
    create.mockResolvedValue(fakeCredential());
    mfaEnrollFinish.mockReturnValue(
      of<AuthResult<StatusResponse>>({ ok: true, value: { status: 'active' } }),
    );

    clickSetup(fixture);
    expect(mfaEnrollBegin).toHaveBeenCalledOnce();
    await flush(fixture);

    expect(create).toHaveBeenCalledOnce();
    const options = create.mock.calls[0][0] as CredentialCreationOptions;
    expect(options.publicKey?.challenge).toBeInstanceOf(Uint8Array);
    expect(options.publicKey?.user.id).toBeInstanceOf(Uint8Array);

    expect(mfaEnrollFinish).toHaveBeenCalledOnce();
    const payload = mfaEnrollFinish.mock.calls[0][0] as {
      id: string;
      rawId: string;
      response: { clientDataJSON: string; attestationObject: string };
    };
    expect(payload.id).toBe('Y3JlZC1pZA');
    expect(typeof payload.rawId).toBe('string');
    expect(typeof payload.response.clientDataJSON).toBe('string');
    expect(typeof payload.response.attestationObject).toBe('string');
    fixture.destroy();
  });

  it('re-enables the action and shows a safe message when the browser ceremony is cancelled', async () => {
    const fixture = configure();
    mfaEnrollBegin.mockReturnValue(
      of<AuthResult<MFAEnrollBeginResponse>>({ ok: true, value: BEGIN_RESPONSE }),
    );
    create.mockRejectedValue(new DOMException('cancelled', 'NotAllowedError'));

    clickSetup(fixture);
    await flush(fixture);

    const el = fixture.nativeElement as HTMLElement;
    expect(el.textContent).toContain('cancelled or could not be completed');
    expect(mfaEnrollFinish).not.toHaveBeenCalled();
    expect(el.querySelector<HTMLButtonElement>('button.primary')?.disabled).toBe(false);
    fixture.destroy();
  });

  it('shows a safe message and does not call finish when the begin call itself fails', async () => {
    const fixture = configure();
    mfaEnrollBegin.mockReturnValue(
      of<AuthResult<MFAEnrollBeginResponse>>({
        ok: false,
        error: { kind: 'mfa_not_eligible', message: 'This account cannot complete this passkey step right now.' },
      }),
    );

    clickSetup(fixture);
    await flush(fixture);

    const el = fixture.nativeElement as HTMLElement;
    expect(el.textContent).toContain('This account cannot complete this passkey step right now.');
    expect(create).not.toHaveBeenCalled();
    fixture.destroy();
  });

  it('routes an unauthenticated (bridging-only) caller to sign-in after a successful finish', async () => {
    const fixture = configure(NOT_AUTHENTICATED);
    mfaEnrollBegin.mockReturnValue(
      of<AuthResult<MFAEnrollBeginResponse>>({ ok: true, value: BEGIN_RESPONSE }),
    );
    create.mockResolvedValue(fakeCredential());
    mfaEnrollFinish.mockReturnValue(
      of<AuthResult<StatusResponse>>({ ok: true, value: { status: 'active' } }),
    );

    clickSetup(fixture);
    await flush(fixture);

    expect(navigateByUrl).toHaveBeenCalledExactlyOnceWith('/mobile/auth/sign-in');
    fixture.destroy();
  });

  it('returns an already-signed-in caller to the authenticated preview area after adding another passkey', async () => {
    const fixture = configure({ ok: true, value: ACTIVE_ACCOUNT });
    mfaEnrollBegin.mockReturnValue(
      of<AuthResult<MFAEnrollBeginResponse>>({ ok: true, value: BEGIN_RESPONSE }),
    );
    create.mockResolvedValue(fakeCredential());
    mfaEnrollFinish.mockReturnValue(
      of<AuthResult<StatusResponse>>({ ok: true, value: { status: 'active' } }),
    );

    clickSetup(fixture);
    await flush(fixture);

    expect(navigateByUrl).toHaveBeenCalledExactlyOnceWith('/mobile/auth/sessions');
    fixture.destroy();
  });

  it('writes no browser storage during a full enrollment cycle', async () => {
    const fixture = configure({ ok: true, value: ACTIVE_ACCOUNT });
    mfaEnrollBegin.mockReturnValue(
      of<AuthResult<MFAEnrollBeginResponse>>({ ok: true, value: BEGIN_RESPONSE }),
    );
    create.mockResolvedValue(fakeCredential());
    mfaEnrollFinish.mockReturnValue(
      of<AuthResult<StatusResponse>>({ ok: true, value: { status: 'active' } }),
    );
    const storageWrite = vi.spyOn(Storage.prototype, 'setItem');

    clickSetup(fixture);
    await flush(fixture);

    expect(storageWrite).not.toHaveBeenCalled();
    storageWrite.mockRestore();
    fixture.destroy();
  });

  it('never renders the raw challenge, attestation, or credential id into the DOM', async () => {
    const fixture = configure();
    mfaEnrollBegin.mockReturnValue(
      of<AuthResult<MFAEnrollBeginResponse>>({ ok: true, value: BEGIN_RESPONSE }),
    );
    create.mockResolvedValue(fakeCredential());
    mfaEnrollFinish.mockReturnValue(
      of<AuthResult<StatusResponse>>({ ok: true, value: { status: 'active' } }),
    );

    clickSetup(fixture);
    await flush(fixture);

    const html = (fixture.nativeElement as HTMLElement).innerHTML;
    expect(html).not.toContain('Y2hhbGxlbmdl');
    expect(html).not.toContain('Y3JlZC1pZA');
    fixture.destroy();
  });
});
