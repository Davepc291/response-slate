import { TestBed } from '@angular/core/testing';
import { provideRouter } from '@angular/router';
import { of } from 'rxjs';

import { AdminListUsersResponse } from '../auth-data/admin-api.models';
import { AdminApiService, AdminResult } from '../auth-data/admin-api.service';
import { AuthSessionState } from '../auth-data/auth-session-state.service';
import { MobileAuthAdminUsers } from './mobile-auth-admin-users';

const USERS: AdminListUsersResponse = {
  users: [
    {
      id: 1,
      email: 'a@example.test',
      display_name: 'Alpha',
      role: 'responder',
      scope: 'engine-1',
      status: 'active',
      created_at: '2026-01-01T00:00:00Z',
    },
  ],
  limit: 25,
  offset: 0,
};

describe('MobileAuthAdminUsers', () => {
  let listUsers: ReturnType<typeof vi.fn<() => ReturnType<AdminApiService['listUsers']>>>;

  function configure(
    accountRole: string | null = 'system_administrator',
    response: AdminResult<AdminListUsersResponse> = { ok: true, value: USERS },
  ) {
    listUsers = vi.fn().mockReturnValue(of<AdminResult<AdminListUsersResponse>>(response));

    TestBed.configureTestingModule({
      imports: [MobileAuthAdminUsers],
      providers: [
        provideRouter([]),
        { provide: AdminApiService, useValue: { listUsers } },
        {
          provide: AuthSessionState,
          useValue: {
            account: () =>
              accountRole
                ? {
                    user_id: 99,
                    email: 'admin@example.test',
                    display_name: 'Admin',
                    role: accountRole,
                    status: 'active',
                  }
                : null,
            refresh: vi.fn(),
          },
        },
      ],
    });
    const fixture = TestBed.createComponent(MobileAuthAdminUsers);
    fixture.detectChanges();
    return fixture;
  }

  it('loads and displays the user list without exposing password/token/session detail', () => {
    const fixture = configure();
    const el = fixture.nativeElement as HTMLElement;
    expect(el.textContent).toContain('Alpha');
    expect(el.textContent).toContain('a@example.test');
    expect(el.innerHTML).not.toMatch(/password_hash|token_digest|session_id|mfa_secret/i);
    fixture.destroy();
  });

  it('shows an Add user link only for an administrator role', () => {
    const fixture = configure('system_administrator');
    const el = fixture.nativeElement as HTMLElement;
    expect(el.textContent).toContain('Add user');
    fixture.destroy();
  });

  it('shows the empty state when no users match', () => {
    const fixture = configure('system_administrator', {
      ok: true,
      value: { users: [], limit: 25, offset: 0 },
    });
    const el = fixture.nativeElement as HTMLElement;
    expect(el.textContent).toContain('No users match');
    fixture.destroy();
  });

  it('shows an unavailable state with retry on backend failure', () => {
    const fixture = configure('system_administrator', {
      ok: false,
      error: { kind: 'unavailable', message: 'unavailable' },
    });
    const el = fixture.nativeElement as HTMLElement;
    expect(el.textContent).toContain('Unable to load users');
    fixture.destroy();
  });

  it('shows an unauthorized state distinctly from a generic failure', () => {
    const fixture = configure('system_administrator', {
      ok: false,
      error: { kind: 'forbidden', message: 'forbidden' },
    });
    const el = fixture.nativeElement as HTMLElement;
    expect(el.textContent).toContain('not authorized');
    fixture.destroy();
  });

  it('applies the search filter and re-issues a bounded request', () => {
    const fixture = configure();
    const input = fixture.nativeElement.querySelector('#admin-users-search') as HTMLInputElement;
    input.value = 'alpha';
    input.dispatchEvent(new Event('input'));
    fixture.detectChanges();

    const form = fixture.nativeElement.querySelector('form.filters') as HTMLFormElement;
    form.dispatchEvent(new Event('submit'));
    fixture.detectChanges();

    expect(listUsers).toHaveBeenLastCalledWith(
      expect.objectContaining({ search: 'alpha', limit: 25, offset: 0 }),
    );
    fixture.destroy();
  });
});
