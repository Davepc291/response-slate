import { DatePipe } from '@angular/common';
import { Component, computed, inject, signal } from '@angular/core';
import { ActivatedRoute, Router, RouterLink } from '@angular/router';

import { HEADER_TITLE } from '../../board-preview/board-preview.fixtures';
import { AdminInvitationResponse, AdminUserView } from '../auth-data/admin-api.models';
import { AdminApiService, AdminResult } from '../auth-data/admin-api.service';
import { AuthSessionState } from '../auth-data/auth-session-state.service';
import { inputValue } from '../auth-data/input-value';

type DangerAction =
  'suspend' | 'disable' | 'restore' | 'revoke-sessions' | 'reset-credential' | 'resend-invitation';

const ACTION_COPY: Record<DangerAction, { verb: string; explanation: string }> = {
  suspend: {
    verb: 'Suspend',
    explanation: 'They will not be able to sign in until restored.',
  },
  disable: {
    verb: 'Disable',
    explanation: 'This blocks sign-in and revokes all active sessions.',
  },
  restore: { verb: 'Restore', explanation: 'This restores access for this account.' },
  'revoke-sessions': {
    verb: 'Revoke all sessions',
    explanation: 'They will need to sign in again everywhere.',
  },
  'reset-credential': {
    verb: 'Reset credential',
    explanation: 'This signs them out everywhere and issues a new one-time invitation code.',
  },
  'resend-invitation': {
    verb: 'Resend invitation',
    explanation: 'The previous invitation code will stop working.',
  },
};

const ALL_ROLES = [
  'system_administrator',
  'department_administrator',
  'dispatcher_operator',
  'responder',
  'read_only_auditor',
];
const NON_ADMINISTRATOR_ROLES = ['dispatcher_operator', 'responder', 'read_only_auditor'];

/**
 * Administrator user-detail/action screen, preview-only at
 * /mobile/auth/admin/users/{id} (Step 9E requirement 10). Every dangerous
 * action requires an explicit, named confirmation
 * (docs/authentication-authorization-v1.md Section 7's proposed dialog
 * text) before the corresponding AdminApiService call is made; the backend
 * still validates and can still reject any of them (self-action, scope,
 * state conflict), which this component surfaces as a generic message,
 * never a raw error.
 */
@Component({
  selector: 'app-mobile-auth-admin-user-detail',
  imports: [DatePipe, RouterLink],
  templateUrl: './mobile-auth-admin-user-detail.html',
  styleUrl: './mobile-auth-admin-user-detail.scss',
})
export class MobileAuthAdminUserDetail {
  private readonly api = inject(AdminApiService);
  private readonly session = inject(AuthSessionState);
  private readonly route = inject(ActivatedRoute);
  private readonly router = inject(Router);

  readonly headerTitle = HEADER_TITLE;
  readonly account = this.session.account;
  readonly userId = Number(this.route.snapshot.paramMap.get('id'));
  readonly actionCopy = ACTION_COPY;

  readonly loading = signal(true);
  readonly notFound = signal(false);
  readonly errorMessage = signal<string | null>(null);
  readonly successMessage = signal<string | null>(null);
  readonly user = signal<AdminUserView | null>(null);
  readonly pendingAction = signal<DangerAction | null>(null);
  readonly busy = signal(false);
  readonly oneTimeCode = signal<{ code: string; expiresAt: string } | null>(null);
  readonly copied = signal(false);

  readonly roleFormOpen = signal(false);
  readonly newRole = signal('');
  readonly newScope = signal('');
  readonly inputValue = inputValue;

  readonly isSelf = computed(() => this.account()?.user_id === this.userId);
  readonly isDepartmentAdmin = computed(() => this.account()?.role === 'department_administrator');
  readonly availableRoles = computed(() =>
    this.isDepartmentAdmin() ? NON_ADMINISTRATOR_ROLES : ALL_ROLES,
  );

  constructor() {
    this.session.refresh();
    this.load();
  }

  private load(): void {
    this.loading.set(true);
    this.api.getUser(this.userId).subscribe((result) => {
      this.loading.set(false);
      if (!result.ok) {
        if (result.error.kind === 'not_found') {
          this.notFound.set(true);
        } else {
          this.errorMessage.set(result.error.message);
        }
        return;
      }
      this.user.set(result.value);
      this.newRole.set(result.value.role);
      this.newScope.set(result.value.scope ?? '');
    });
  }

  requestAction(action: DangerAction): void {
    this.errorMessage.set(null);
    this.successMessage.set(null);
    this.pendingAction.set(action);
  }

  cancelAction(): void {
    this.pendingAction.set(null);
  }

  confirmAction(): void {
    const action = this.pendingAction();
    if (!action || this.busy()) {
      return;
    }
    this.busy.set(true);
    this.pendingAction.set(null);

    const onSuccess = (message: string) => {
      this.busy.set(false);
      this.successMessage.set(message);
      this.load();
    };
    const onFailure = (message: string) => {
      this.busy.set(false);
      this.errorMessage.set(message);
    };
    const onCode = (result: AdminResult<AdminInvitationResponse>) => {
      this.busy.set(false);
      if (!result.ok) {
        this.errorMessage.set(result.error.message);
        return;
      }
      this.oneTimeCode.set({
        code: result.value.invitation.code,
        expiresAt: result.value.invitation.expires_at,
      });
    };

    switch (action) {
      case 'suspend':
        this.api
          .suspend(this.userId)
          .subscribe((r) => (r.ok ? onSuccess('Account suspended.') : onFailure(r.error.message)));
        break;
      case 'disable':
        this.api
          .disable(this.userId)
          .subscribe((r) => (r.ok ? onSuccess('Account disabled.') : onFailure(r.error.message)));
        break;
      case 'restore':
        this.api
          .restore(this.userId)
          .subscribe((r) => (r.ok ? onSuccess('Account restored.') : onFailure(r.error.message)));
        break;
      case 'revoke-sessions':
        this.api
          .revokeSessions(this.userId)
          .subscribe((r) =>
            r.ok
              ? onSuccess(`All sessions revoked (${r.value.revoked_count}).`)
              : onFailure(r.error.message),
          );
        break;
      case 'resend-invitation':
        this.api.resendInvitation(this.userId).subscribe(onCode);
        break;
      case 'reset-credential':
        this.api.resetCredential(this.userId).subscribe(onCode);
        break;
    }
  }

  async copyCode(): Promise<void> {
    const value = this.oneTimeCode();
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

  dismissCode(): void {
    this.oneTimeCode.set(null);
    this.copied.set(false);
    this.load();
  }

  openRoleForm(): void {
    this.roleFormOpen.set(true);
    this.errorMessage.set(null);
    this.successMessage.set(null);
  }

  cancelRoleForm(): void {
    this.roleFormOpen.set(false);
    const u = this.user();
    if (u) {
      this.newRole.set(u.role);
      this.newScope.set(u.scope ?? '');
    }
  }

  submitRoleChange(event: Event): void {
    event.preventDefault();
    if (this.busy()) {
      return;
    }
    this.busy.set(true);
    this.api
      .changeRole(this.userId, { role: this.newRole(), scope: this.newScope().trim() })
      .subscribe((r) => {
        this.busy.set(false);
        if (!r.ok) {
          this.errorMessage.set(r.error.message);
          return;
        }
        this.roleFormOpen.set(false);
        this.successMessage.set('Role/scope updated.');
        this.load();
      });
  }

  backToList(): void {
    void this.router.navigateByUrl('/mobile/auth/admin/users');
  }
}
