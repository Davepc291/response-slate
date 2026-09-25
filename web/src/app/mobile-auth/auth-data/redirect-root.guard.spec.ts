import { TestBed } from '@angular/core/testing';
import { Router, UrlTree } from '@angular/router';
import { Observable, firstValueFrom, of } from 'rxjs';

import { AuthResult } from './auth-api.service';
import { AuthApiService } from './auth-api.service';
import { MeResponse } from './auth-api.models';
import { redirectRoot } from './redirect-root.guard';

describe('redirectRoot', () => {
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

  function run(): Promise<UrlTree> {
    const result = TestBed.runInInjectionContext(() =>
      redirectRoot({} as never, {} as never),
    ) as Observable<UrlTree>;
    return firstValueFrom(result);
  }

  it('sends an authenticated visitor into the protected mobile area', async () => {
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
    expect(createUrlTree).toHaveBeenCalledExactlyOnceWith(['/mobile/home']);
    expect(result).toBe('URL_TREE');
  });

  it('sends an unauthenticated visitor to sign-in', async () => {
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
