import { TestBed } from '@angular/core/testing';
import { Router, UrlTree } from '@angular/router';
import { Observable, firstValueFrom, of } from 'rxjs';

import { AuthResult } from './auth-api.service';
import { AuthApiService } from './auth-api.service';
import { MeResponse } from './auth-api.models';
import { requireAdminRole } from './require-admin-role.guard';

describe('requireAdminRole', () => {
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
      requireAdminRole({} as never, {} as never),
    ) as Observable<boolean | UrlTree>;
    return firstValueFrom(result);
  }

  function meResult(role: string): AuthResult<MeResponse> {
    return {
      ok: true,
      value: { user_id: 1, email: 'a@example.com', display_name: 'A', role, status: 'active' },
    };
  }

  it('allows activation for a system administrator', async () => {
    me.mockReturnValue(of(meResult('system_administrator')));
    expect(await run()).toBe(true);
  });

  it('allows activation for a department administrator', async () => {
    me.mockReturnValue(of(meResult('department_administrator')));
    expect(await run()).toBe(true);
  });

  it('redirects a non-administrator to the access-denied screen, never granting access', async () => {
    me.mockReturnValue(of(meResult('responder')));
    const result = await run();
    expect(createUrlTree).toHaveBeenCalledExactlyOnceWith(['/mobile/auth/admin/access-denied']);
    expect(result).toBe('URL_TREE');
  });

  it('redirects an unauthenticated caller to sign-in', async () => {
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
