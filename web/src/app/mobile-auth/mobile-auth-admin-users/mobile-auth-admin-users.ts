import { Component, inject, signal } from '@angular/core';
import { RouterLink } from '@angular/router';

import { HEADER_TITLE } from '../../board-preview/board-preview.fixtures';
import { AdminUserView } from '../auth-data/admin-api.models';
import { AdminApiService } from '../auth-data/admin-api.service';
import { AuthSessionState } from '../auth-data/auth-session-state.service';
import { inputValue } from '../auth-data/input-value';

const PAGE_SIZE = 25;
const ROLE_OPTIONS = [
  'system_administrator',
  'department_administrator',
  'dispatcher_operator',
  'responder',
  'read_only_auditor',
];
const STATUS_OPTIONS = [
  'invited',
  'password_change_required',
  'active',
  'suspended',
  'disabled',
  'expired',
];

type ListState = 'loading' | 'ready' | 'empty' | 'unavailable' | 'unauthorized';

/**
 * Administrator Users list, preview-only at /mobile/auth/admin/users (Step
 * 9E requirement 8). Reachable only behind requireVerifiedSession and
 * requireAdminRole; the backend's own ManageUsers permission check is what
 * actually scopes results (a department administrator's own scope) —
 * this component never filters or hides a row client-side for
 * authorization reasons, only for the search/role/status UX filters the
 * administrator explicitly chose. Network-only: no user list is ever
 * cached (see AdminApiService's doc comment) and this component makes no
 * attempt to serve stale data when offline.
 */
@Component({
  selector: 'app-mobile-auth-admin-users',
  imports: [RouterLink],
  templateUrl: './mobile-auth-admin-users.html',
  styleUrl: './mobile-auth-admin-users.scss',
})
export class MobileAuthAdminUsers {
  private readonly api = inject(AdminApiService);
  private readonly session = inject(AuthSessionState);

  readonly headerTitle = HEADER_TITLE;
  readonly account = this.session.account;
  readonly roleOptions = ROLE_OPTIONS;
  readonly statusOptions = STATUS_OPTIONS;

  readonly state = signal<ListState>('loading');
  readonly users = signal<AdminUserView[]>([]);
  readonly search = signal('');
  readonly roleFilter = signal('');
  readonly statusFilter = signal('');
  readonly offset = signal(0);
  readonly inputValue = inputValue;

  constructor() {
    this.session.refresh();
    this.load();
  }

  private load(): void {
    this.state.set('loading');
    this.api
      .listUsers({
        search: this.search().trim() || undefined,
        role: this.roleFilter() || undefined,
        status: this.statusFilter() || undefined,
        limit: PAGE_SIZE,
        offset: this.offset(),
      })
      .subscribe((result) => {
        if (!result.ok) {
          this.state.set(
            result.error.kind === 'forbidden' || result.error.kind === 'not_authenticated'
              ? 'unauthorized'
              : 'unavailable',
          );
          return;
        }
        this.users.set(result.value.users);
        this.state.set(result.value.users.length === 0 ? 'empty' : 'ready');
      });
  }

  applyFilters(event: Event): void {
    event.preventDefault();
    this.offset.set(0);
    this.load();
  }

  retry(): void {
    this.load();
  }

  nextPage(): void {
    this.offset.set(this.offset() + PAGE_SIZE);
    this.load();
  }

  previousPage(): void {
    this.offset.set(Math.max(0, this.offset() - PAGE_SIZE));
    this.load();
  }

  canAddUser(): boolean {
    const role = this.account()?.role;
    return role === 'system_administrator' || role === 'department_administrator';
  }
}
