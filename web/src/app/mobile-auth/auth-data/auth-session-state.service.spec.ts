import { TestBed } from '@angular/core/testing';
import { of } from 'rxjs';

import { AuthResult } from './auth-api.service';
import { AuthApiService } from './auth-api.service';
import { MeResponse } from './auth-api.models';
import { AuthSessionState } from './auth-session-state.service';

const ACCOUNT: MeResponse = {
  user_id: 1,
  email: 'a@example.com',
  display_name: 'A',
  role: 'responder',
  status: 'active',
};

describe('AuthSessionState', () => {
  let me: ReturnType<typeof vi.fn<() => ReturnType<AuthApiService['me']>>>;

  function configure(): AuthSessionState {
    me = vi.fn();
    TestBed.configureTestingModule({
      providers: [{ provide: AuthApiService, useValue: { me } }],
    });
    return TestBed.inject(AuthSessionState);
  }

  it('starts in the checking state with no cached account', () => {
    const state = configure();
    expect(state.status()).toBe('checking');
    expect(state.account()).toBeNull();
    expect(state.isAuthenticated()).toBe(false);
  });

  it('moves to authenticated on a verified /api/auth/me response', () => {
    const state = configure();
    me.mockReturnValue(of<AuthResult<MeResponse>>({ ok: true, value: ACCOUNT }));
    state.refresh();
    expect(state.status()).toBe('authenticated');
    expect(state.account()).toEqual(ACCOUNT);
    expect(state.isAuthenticated()).toBe(true);
  });

  it('moves to unauthenticated on not_authenticated', () => {
    const state = configure();
    me.mockReturnValue(
      of<AuthResult<MeResponse>>({
        ok: false,
        error: { kind: 'not_authenticated', message: 'Sign-in required.' },
      }),
    );
    state.refresh();
    expect(state.status()).toBe('unauthenticated');
    expect(state.account()).toBeNull();
  });

  it('moves to expired on session_expired', () => {
    const state = configure();
    me.mockReturnValue(
      of<AuthResult<MeResponse>>({
        ok: false,
        error: {
          kind: 'session_expired',
          message: 'Your session has expired. Please sign in again.',
        },
      }),
    );
    state.refresh();
    expect(state.status()).toBe('expired');
  });

  it('moves to unavailable on a backend/service failure', () => {
    const state = configure();
    me.mockReturnValue(
      of<AuthResult<MeResponse>>({
        ok: false,
        error: { kind: 'unavailable', message: 'Sign-in is temporarily unavailable.' },
      }),
    );
    state.refresh();
    expect(state.status()).toBe('unavailable');
  });

  it('clear() returns to unauthenticated with no account', () => {
    const state = configure();
    state.setAuthenticated(ACCOUNT);
    expect(state.isAuthenticated()).toBe(true);
    state.clear();
    expect(state.status()).toBe('unauthenticated');
    expect(state.account()).toBeNull();
  });

  it('writes no browser storage while transitioning state', () => {
    const state = configure();
    const storageWrite = vi.spyOn(Storage.prototype, 'setItem');
    me.mockReturnValue(of<AuthResult<MeResponse>>({ ok: true, value: ACCOUNT }));
    state.refresh();
    state.setAuthenticated(ACCOUNT);
    state.clear();
    expect(storageWrite).not.toHaveBeenCalled();
    storageWrite.mockRestore();
  });
});
