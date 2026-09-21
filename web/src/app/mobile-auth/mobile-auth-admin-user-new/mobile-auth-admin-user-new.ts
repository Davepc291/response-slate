import { Component, computed, inject, signal } from '@angular/core';
import { Router, RouterLink } from '@angular/router';

import { HEADER_TITLE } from '../../board-preview/board-preview.fixtures';
import { AdminApiService } from '../auth-data/admin-api.service';
import { AuthSessionState } from '../auth-data/auth-session-state.service';
import { inputValue } from '../auth-data/input-value';

const NON_ADMINISTRATOR_ROLES = ['dispatcher_operator', 'responder', 'read_only_auditor'];
const ALL_ROLES = [
  'system_administrator',
  'department_administrator',
  'dispatcher_operator',
  'responder',
  'read_only_auditor',
];

interface OneTimeCode {
  code: string;
  expiresAt: string;
  email: string;
}

/**
 * Add-user screen, preview-only at /mobile/auth/admin/users/new (Step 9E
 * requirement 9). There is no password field: the backend generates the
 * single-use invitation (Section 2/7). The role selector hides roles a
 * department administrator cannot assign for UX only — the backend's own
 * authorization.CanAssignRole check is what actually enforces this, and a
 * direct API request bypassing this UI is rejected there regardless of
 * what this component displays. The one-time invitation code, once
 * received, exists only in this component's own signal state: it is never
 * written to any browser storage, query string, or route state, and is
 * cleared for good the moment this component is destroyed or the code is
 * dismissed (dismiss() below navigates away, which destroys this
 * component and its signals).
 */
@Component({
  selector: 'app-mobile-auth-admin-user-new',
  imports: [RouterLink],
  templateUrl: './mobile-auth-admin-user-new.html',
  styleUrl: './mobile-auth-admin-user-new.scss',
})
export class MobileAuthAdminUserNew {
  private readonly api = inject(AdminApiService);
  private readonly session = inject(AuthSessionState);
  private readonly router = inject(Router);

  readonly headerTitle = HEADER_TITLE;
  readonly account = this.session.account;

  readonly email = signal('');
  readonly displayName = signal('');
  readonly role = signal('');
  readonly scope = signal('');
  readonly confirmCreate = signal(false);
  readonly confirmSystemAdmin = signal(false);
  readonly submitting = signal(false);
  readonly errorMessage = signal<string | null>(null);
  readonly result = signal<OneTimeCode | null>(null);
  readonly copied = signal(false);
  readonly delivered = signal(false);

  readonly inputValue = inputValue;

  readonly isDepartmentAdmin = computed(() => this.account()?.role === 'department_administrator');
  readonly availableRoles = computed(() =>
    this.isDepartmentAdmin() ? NON_ADMINISTRATOR_ROLES : ALL_ROLES,
  );
  readonly isSystemAdministratorRole = computed(() => this.role() === 'system_administrator');

  constructor() {
    this.session.refresh();
  }

  submit(event: Event): void {
    event.preventDefault();
    if (this.submitting() || this.result()) {
      return;
    }
    const email = this.email().trim();
    const displayName = this.displayName().trim();
    const role = this.role();

    if (!email || !displayName || !role) {
      this.errorMessage.set('Fill in every field and choose a role.');
      return;
    }
    if (!this.confirmCreate()) {
      this.errorMessage.set('Confirm that you understand how this account is created.');
      return;
    }
    if (role === 'system_administrator' && !this.confirmSystemAdmin()) {
      this.errorMessage.set('Confirm that you intend to create a system administrator.');
      return;
    }

    this.submitting.set(true);
    this.errorMessage.set(null);
    this.api
      .createUser({ email, display_name: displayName, role, scope: this.scope().trim() })
      .subscribe((res) => {
        this.submitting.set(false);
        if (!res.ok) {
          this.errorMessage.set(res.error.message);
          return;
        }
        this.result.set({
          code: res.value.invitation.code,
          expiresAt: res.value.invitation.expires_at,
          email: res.value.user.email,
        });
      });
  }

  async copyCode(): Promise<void> {
    const value = this.result();
    if (!value || !navigator.clipboard) {
      return;
    }
    try {
      await navigator.clipboard.writeText(value.code);
      this.copied.set(true);
    } catch {
      this.copied.set(false);
    }
  }

  markDelivered(): void {
    this.delivered.set(true);
  }

  dismiss(): void {
    // Navigating away destroys this component and every signal it holds;
    // the raw code is never recoverable after this point (Step 9E
    // requirement: "Once dismissed or navigated away, the UI must not
    // recover or redisplay the code").
    void this.router.navigateByUrl('/mobile/auth/admin/users');
  }
}
