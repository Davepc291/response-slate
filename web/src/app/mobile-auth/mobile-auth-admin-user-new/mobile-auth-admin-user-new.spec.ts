import { TestBed } from '@angular/core/testing';
import { Router, provideRouter } from '@angular/router';
import { of } from 'rxjs';

import { AdminCreateUserResponse } from '../auth-data/admin-api.models';
import { AdminApiService, AdminResult } from '../auth-data/admin-api.service';
import { AuthSessionState } from '../auth-data/auth-session-state.service';
import { MobileAuthAdminUserNew } from './mobile-auth-admin-user-new';

const CREATE_RESPONSE: AdminCreateUserResponse = {
  user: {
    id: 5,
    email: 'new@example.test',
    display_name: 'New',
    role: 'responder',
    scope: 'engine-1',
    status: 'invited',
    created_at: '2026-01-01T00:00:00Z',
  },
  invitation: {
    code: 'raw-one-time-code-abc',
    expires_at: '2026-01-02T00:00:00Z',
    sensitive: true,
    warning: 'Sensitive — shown once.',
  },
};

describe('MobileAuthAdminUserNew', () => {
  let createUser: ReturnType<typeof vi.fn<() => ReturnType<AdminApiService['createUser']>>>;
  let navigateByUrl: ReturnType<typeof vi.fn<() => Promise<boolean>>>;

  function configure(accountRole = 'system_administrator') {
    createUser = vi
      .fn()
      .mockReturnValue(
        of<AdminResult<AdminCreateUserResponse>>({ ok: true, value: CREATE_RESPONSE }),
      );

    TestBed.configureTestingModule({
      imports: [MobileAuthAdminUserNew],
      providers: [
        provideRouter([]),
        { provide: AdminApiService, useValue: { createUser } },
        {
          provide: AuthSessionState,
          useValue: {
            account: () => ({
              user_id: 1,
              email: 'admin@example.test',
              display_name: 'Admin',
              role: accountRole,
              status: 'active',
            }),
            refresh: vi.fn(),
          },
        },
      ],
    });
    // RouterLink needs a real Router; spy on it instead of replacing the
    // Router token (see mobile-auth-forgot-password.spec.ts for why).
    navigateByUrl = vi
      .spyOn(TestBed.inject(Router), 'navigateByUrl')
      .mockResolvedValue(true) as never;
    const fixture = TestBed.createComponent(MobileAuthAdminUserNew);
    fixture.detectChanges();
    return fixture;
  }

  function fillForm(el: HTMLElement, role = 'responder') {
    const email = el.querySelector('#admin-new-email') as HTMLInputElement;
    email.value = 'new@example.test';
    email.dispatchEvent(new Event('input'));
    const name = el.querySelector('#admin-new-name') as HTMLInputElement;
    name.value = 'New';
    name.dispatchEvent(new Event('input'));
    const roleSelect = el.querySelector('#admin-new-role') as HTMLSelectElement;
    roleSelect.value = role;
    roleSelect.dispatchEvent(new Event('change'));
  }

  it('explains there is no public registration and that the code is shown once', () => {
    const fixture = configure();
    const el = fixture.nativeElement as HTMLElement;
    expect(el.textContent).toContain('no public registration');
    fixture.destroy();
  });

  it('hides administrator roles from a department administrator', () => {
    const fixture = configure('department_administrator');
    const el = fixture.nativeElement as HTMLElement;
    const options = Array.from(el.querySelectorAll('#admin-new-role option')).map((o) =>
      o.getAttribute('value'),
    );
    expect(options).not.toContain('system_administrator');
    expect(options).not.toContain('department_administrator');
    fixture.destroy();
  });

  it('requires the general confirmation checkbox before submitting', () => {
    const fixture = configure();
    const el = fixture.nativeElement as HTMLElement;
    fillForm(el);
    (el.querySelector('form') as HTMLFormElement).dispatchEvent(new Event('submit'));
    fixture.detectChanges();

    expect(createUser).not.toHaveBeenCalled();
    expect(el.textContent).toContain('Confirm that you understand');
    fixture.destroy();
  });

  it('requires an additional confirmation before creating a system administrator', () => {
    const fixture = configure();
    const el = fixture.nativeElement as HTMLElement;
    fillForm(el, 'system_administrator');
    fixture.detectChanges();
    const generalCheckbox = el.querySelectorAll('input[type="checkbox"]')[0] as HTMLInputElement;
    generalCheckbox.dispatchEvent(new Event('change'));
    fixture.detectChanges();

    (el.querySelector('form') as HTMLFormElement).dispatchEvent(new Event('submit'));
    fixture.detectChanges();

    expect(createUser).not.toHaveBeenCalled();
    expect(el.textContent).toContain('intend to create a system administrator');
    fixture.destroy();
  });

  it('submits once every confirmation is given and displays the one-time code panel', () => {
    const fixture = configure();
    const el = fixture.nativeElement as HTMLElement;
    fillForm(el);
    const generalCheckbox = el.querySelectorAll('input[type="checkbox"]')[0] as HTMLInputElement;
    generalCheckbox.dispatchEvent(new Event('change'));
    fixture.detectChanges();

    (el.querySelector('form') as HTMLFormElement).dispatchEvent(new Event('submit'));
    fixture.detectChanges();

    expect(createUser).toHaveBeenCalledOnce();
    expect(el.textContent).toContain('raw-one-time-code-abc');
    expect(el.textContent).toContain('Sensitive');
    fixture.destroy();
  });

  it('prevents a duplicate submission while a create request is in flight', () => {
    const fixture = configure();
    const el = fixture.nativeElement as HTMLElement;
    fillForm(el);
    const generalCheckbox = el.querySelectorAll('input[type="checkbox"]')[0] as HTMLInputElement;
    generalCheckbox.dispatchEvent(new Event('change'));
    fixture.detectChanges();

    const form = el.querySelector('form') as HTMLFormElement;
    form.dispatchEvent(new Event('submit'));
    form.dispatchEvent(new Event('submit'));
    fixture.detectChanges();

    expect(createUser).toHaveBeenCalledOnce();
    fixture.destroy();
  });

  it('never automatically copies the code to the clipboard', () => {
    const writeText = vi.fn().mockResolvedValue(undefined);
    Object.assign(navigator, { clipboard: { writeText } });

    const fixture = configure();
    const el = fixture.nativeElement as HTMLElement;
    fillForm(el);
    (el.querySelectorAll('input[type="checkbox"]')[0] as HTMLInputElement).dispatchEvent(
      new Event('change'),
    );
    fixture.detectChanges();
    (el.querySelector('form') as HTMLFormElement).dispatchEvent(new Event('submit'));
    fixture.detectChanges();

    expect(writeText).not.toHaveBeenCalled();
    fixture.destroy();
  });

  it('navigates away (destroying the code) once delivery is confirmed and dismissed', () => {
    const fixture = configure();
    const el = fixture.nativeElement as HTMLElement;
    fillForm(el);
    (el.querySelectorAll('input[type="checkbox"]')[0] as HTMLInputElement).dispatchEvent(
      new Event('change'),
    );
    fixture.detectChanges();
    (el.querySelector('form') as HTMLFormElement).dispatchEvent(new Event('submit'));
    fixture.detectChanges();

    const deliveredButton = Array.from(el.querySelectorAll('button')).find((b) =>
      b.textContent?.includes('I have delivered this code securely'),
    );
    deliveredButton?.dispatchEvent(new Event('click'));
    fixture.detectChanges();

    const doneButton = Array.from(el.querySelectorAll('button')).find((b) =>
      b.textContent?.includes('Done'),
    );
    doneButton?.dispatchEvent(new Event('click'));

    expect(navigateByUrl).toHaveBeenCalledExactlyOnceWith('/mobile/auth/admin/users');
    fixture.destroy();
  });
});
