import { TestBed } from '@angular/core/testing';
import { Router, UrlTree } from '@angular/router';
import { Observable, firstValueFrom, of } from 'rxjs';

import { AuthResult } from './auth-api.service';
import { AuthApiService } from './auth-api.service';
import { MeResponse } from './auth-api.models';
import { requireVerifiedSession } from './require-verified-session.guard';

describe('requireVerifiedSession', () => {
  let me: ReturnType<typeof vi.fn<() => ReturnType<AuthApiService['me']>>>;
  let createUrlTree: ReturnType<typeof vi.fn>;

  beforeEach(() => {
    me = vi.fn();
    createUrlTree = vi.fn().mockReturnValue('URL_TREE');
    TestBed.configureTestingModule({
      providers: [
        { provide: AuthApiService, useValue: { me } },
        { provide: Router, useValue: { createUrlTree } },
      ],
    });
  });

  function run(): Promise<boolean | UrlTree> {
    const result = TestBed.runInInjectionContext(() =>
      requireVerifiedSession({} as never, {} as never),
    ) as Observable<boolean | UrlTree>;
    return firstValueFrom(result);
  }

  it('allows activation when /api/auth/me resolves ok', async () => {
    me.mockReturnValue(
      of<AuthResult<MeResponse>>({
        ok: true,
        value: {
          user_id: 1,
          email: 'a@example.com',
          display_name: 'A',
          role: 'responder',
          status: 'active',
        },
      }),
    );
    const result = await run();
    expect(result).toBe(true);
  });

  it('redirects to sign-in when /api/auth/me fails', async () => {
    me.mockReturnValue(
      of<AuthResult<MeResponse>>({
        ok: false,
        error: { kind: 'not_authenticated', message: 'Sign-in required.' },
      }),
    );
    const result = await run();
    expect(createUrlTree).toHaveBeenCalledExactlyOnceWith(['/mobile/auth/sign-in']);
    expect(result).toBe('URL_TREE');
  });
});
