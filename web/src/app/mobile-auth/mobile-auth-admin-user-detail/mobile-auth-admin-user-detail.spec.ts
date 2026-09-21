import { TestBed } from '@angular/core/testing';
import { ActivatedRoute, provideRouter } from '@angular/router';
import { of } from 'rxjs';

import {
  AdminInvitationResponse,
  AdminStatusResponse,
  AdminUserView,
} from '../auth-data/admin-api.models';
import { AdminApiService, AdminResult } from '../auth-data/admin-api.service';
import { AuthSessionState } from '../auth-data/auth-session-state.service';
import { MobileAuthAdminUserDetail } from './mobile-auth-admin-user-detail';

const USER: AdminUserView = {
  id: 5,
  email: 'target@example.test',
  display_name: 'Target User',
  role: 'responder',
  scope: 'engine-1',
  status: 'active',
  created_at: '2026-01-01T00:00:00Z',
};

function activatedRouteFor(id: string) {
  return { snapshot: { paramMap: { get: () => id } } };
}

describe('MobileAuthAdminUserDetail', () => {
  let getUser: ReturnType<typeof vi.fn<() => ReturnType<AdminApiService['getUser']>>>;
  let suspend: ReturnType<typeof vi.fn<() => ReturnType<AdminApiService['suspend']>>>;
  let resendInvitation: ReturnType<
    typeof vi.fn<() => ReturnType<AdminApiService['resendInvitation']>>
  >;

  function configure(selfUserId = 1) {
    getUser = vi.fn().mockReturnValue(of<AdminResult<AdminUserView>>({ ok: true, value: USER }));
    suspend = vi.fn();
    resendInvitation = vi.fn();

    TestBed.configureTestingModule({
      imports: [MobileAuthAdminUserDetail],
      providers: [
        provideRouter([]),
        { provide: ActivatedRoute, useValue: activatedRouteFor('5') },
        {
          provide: AdminApiService,
          useValue: {
            getUser,
            suspend,
            disable: vi.fn(),
            restore: vi.fn(),
            revokeSessions: vi.fn(),
            resendInvitation,
            resetCredential: vi.fn(),
            changeRole: vi.fn(),
          },
        },
        {
          provide: AuthSessionState,
          useValue: {
            account: () => ({
              user_id: selfUserId,
              email: 'admin@example.test',
              display_name: 'Admin',
              role: 'system_administrator',
              status: 'active',
            }),
            refresh: vi.fn(),
          },
        },
      ],
    });
    const fixture = TestBed.createComponent(MobileAuthAdminUserDetail);
    fixture.detectChanges();
    return fixture;
  }

  it('displays safe user detail fields without password/token detail', () => {
    const fixture = configure();
    const el = fixture.nativeElement as HTMLElement;
    expect(el.textContent).toContain('Target User');
    expect(el.textContent).toContain('target@example.test');
    expect(el.innerHTML).not.toMatch(/password|token|session_id/i);
    fixture.destroy();
  });

  it('disables dangerous self-actions when viewing your own account', () => {
    const fixture = configure(5);
    const el = fixture.nativeElement as HTMLElement;
    const suspendButton = Array.from(el.querySelectorAll('button')).find(
      (b) => b.textContent?.trim() === 'Suspend',
    ) as HTMLButtonElement;
    expect(suspendButton.disabled).toBe(true);
    fixture.destroy();
  });

  it('requires an explicit confirmation naming the target user before suspending', () => {
    const fixture = configure(1);
    const el = fixture.nativeElement as HTMLElement;
    const suspendButton = Array.from(el.querySelectorAll('button')).find(
      (b) => b.textContent?.trim() === 'Suspend',
    ) as HTMLButtonElement;
    suspendButton.dispatchEvent(new Event('click'));
    fixture.detectChanges();

    expect(suspend).not.toHaveBeenCalled();
    const dialog = el.querySelector('.confirm-dialog') as HTMLElement;
    expect(dialog.textContent).toContain('Target User');
    expect(dialog.textContent).toContain('target@example.test');

    const confirmButton = Array.from(dialog.querySelectorAll('button')).find(
      (b) => b.textContent?.trim() === 'Suspend',
    ) as HTMLButtonElement;
    suspend.mockReturnValue(
      of<AdminResult<AdminStatusResponse>>({ ok: true, value: { status: 'suspended' } }),
    );
    confirmButton.dispatchEvent(new Event('click'));

    expect(suspend).toHaveBeenCalledExactlyOnceWith(5);
    fixture.destroy();
  });

  it('cancels a pending confirmation without calling the API', () => {
    const fixture = configure(1);
    const el = fixture.nativeElement as HTMLElement;
    const suspendButton = Array.from(el.querySelectorAll('button')).find(
      (b) => b.textContent?.trim() === 'Suspend',
    ) as HTMLButtonElement;
    suspendButton.dispatchEvent(new Event('click'));
    fixture.detectChanges();

    const cancelButton = Array.from(el.querySelectorAll('.confirm-dialog button')).find(
      (b) => b.textContent?.trim() === 'Cancel',
    ) as HTMLButtonElement;
    cancelButton.dispatchEvent(new Event('click'));
    fixture.detectChanges();

    expect(el.querySelector('.confirm-dialog')).toBeNull();
    expect(suspend).not.toHaveBeenCalled();
    fixture.destroy();
  });

  it('handles a stale/conflict response generically', () => {
    const fixture = configure(1);
    suspend.mockReturnValue(
      of<AdminResult<AdminStatusResponse>>({
        ok: false,
        error: {
          kind: 'invalid_state',
          message: "This action is not permitted for the account's current state.",
        },
      }),
    );
    const el = fixture.nativeElement as HTMLElement;
    (
      Array.from(el.querySelectorAll('button')).find(
        (b) => b.textContent?.trim() === 'Suspend',
      ) as HTMLButtonElement
    ).dispatchEvent(new Event('click'));
    fixture.detectChanges();
    (
      Array.from(el.querySelectorAll('.confirm-dialog button')).find(
        (b) => b.textContent?.trim() === 'Suspend',
      ) as HTMLButtonElement
    ).dispatchEvent(new Event('click'));
    fixture.detectChanges();

    expect(el.textContent).toContain("not permitted for the account's current state");
    fixture.destroy();
  });

  it('shows the one-time invitation panel for resend and clears it on dismissal', () => {
    const fixture = configure(1);
    resendInvitation.mockReturnValue(
      of<AdminResult<AdminInvitationResponse>>({
        ok: true,
        value: {
          invitation: {
            code: 'resend-code-xyz',
            expires_at: '2026-01-03T00:00:00Z',
            sensitive: true,
            warning: 'w',
          },
        },
      }),
    );
    const el = fixture.nativeElement as HTMLElement;
    (
      Array.from(el.querySelectorAll('button')).find((b) =>
        b.textContent?.includes('Resend invitation'),
      ) as HTMLButtonElement
    ).dispatchEvent(new Event('click'));
    fixture.detectChanges();
    (
      Array.from(el.querySelectorAll('.confirm-dialog button')).find((b) =>
        b.textContent?.includes('Resend invitation'),
      ) as HTMLButtonElement
    ).dispatchEvent(new Event('click'));
    fixture.detectChanges();

    expect(el.textContent).toContain('resend-code-xyz');

    (
      Array.from(el.querySelectorAll('button')).find((b) =>
        b.textContent?.includes('I have delivered this code securely'),
      ) as HTMLButtonElement
    ).dispatchEvent(new Event('click'));
    fixture.detectChanges();

    expect(el.textContent).not.toContain('resend-code-xyz');
    fixture.destroy();
  });

  it('shows a not-found message for an out-of-scope or unknown user', () => {
    getUser = vi.fn().mockReturnValue(
      of<AdminResult<AdminUserView>>({
        ok: false,
        error: { kind: 'not_found', message: 'User not found.' },
      }),
    );
    TestBed.configureTestingModule({
      imports: [MobileAuthAdminUserDetail],
      providers: [
        provideRouter([]),
        { provide: ActivatedRoute, useValue: activatedRouteFor('999') },
        {
          provide: AdminApiService,
          useValue: {
            getUser,
            suspend: vi.fn(),
            disable: vi.fn(),
            restore: vi.fn(),
            revokeSessions: vi.fn(),
            resendInvitation: vi.fn(),
            resetCredential: vi.fn(),
            changeRole: vi.fn(),
          },
        },
        {
          provide: AuthSessionState,
          useValue: {
            account: () => ({
              user_id: 1,
              email: 'a@example.test',
              display_name: 'A',
              role: 'system_administrator',
              status: 'active',
            }),
            refresh: vi.fn(),
          },
        },
      ],
    });
    const fixture = TestBed.createComponent(MobileAuthAdminUserDetail);
    fixture.detectChanges();
    const el = fixture.nativeElement as HTMLElement;
    expect(el.textContent).toContain('not found');
    fixture.destroy();
  });
});
