import { DatePipe } from '@angular/common';
import { Component, inject, signal } from '@angular/core';
import { Router } from '@angular/router';

import { HEADER_TITLE } from '../../board-preview/board-preview.fixtures';
import { SessionView } from '../auth-data/auth-api.models';
import { AuthApiService } from '../auth-data/auth-api.service';
import { AuthSessionState } from '../auth-data/auth-session-state.service';

/**
 * Session-management preview at /mobile/auth/sessions (Step 9D requirement
 * 9). Reachable only after requireVerifiedSession's route guard confirms a
 * live GET /api/auth/me session. Every state-changing action here (revoke,
 * logout, logout-all) goes through AuthApiService, which the CSRF
 * interceptor covers automatically for these exact routes. It shows only
 * device_hint and timestamps already deemed safe by the backend's
 * sessionView shape — never a cookie value, token digest, or IP address.
 */
@Component({
  selector: 'app-mobile-auth-sessions',
  imports: [DatePipe],
  templateUrl: './mobile-auth-sessions.html',
  styleUrl: './mobile-auth-sessions.scss',
})
export class MobileAuthSessions {
  private readonly api = inject(AuthApiService);
  private readonly session = inject(AuthSessionState);
  private readonly router = inject(Router);

  readonly headerTitle = HEADER_TITLE;
  readonly account = this.session.account;
  readonly loading = signal(true);
  readonly errorMessage = signal<string | null>(null);
  readonly sessions = signal<SessionView[]>([]);
  readonly busySessionId = signal<number | null>(null);
  readonly signingOut = signal(false);

  constructor() {
    this.session.refresh();
    this.loadSessions();
  }

  private loadSessions(): void {
    this.loading.set(true);
    this.errorMessage.set(null);
    this.api.sessions().subscribe((result) => {
      this.loading.set(false);
      if (!result.ok) {
        this.errorMessage.set('Unable to load sessions right now.');
        return;
      }
      this.sessions.set(result.value);
    });
  }

  revoke(id: number): void {
    if (this.busySessionId() !== null) {
      return;
    }
    this.busySessionId.set(id);
    this.errorMessage.set(null);
    this.api.revokeSession(id).subscribe((result) => {
      this.busySessionId.set(null);
      if (!result.ok) {
        this.errorMessage.set('Unable to revoke that session right now.');
        return;
      }
      this.loadSessions();
    });
  }

  signOut(): void {
    if (this.signingOut()) {
      return;
    }
    this.signingOut.set(true);
    this.api.logout().subscribe(() => {
      this.session.clear();
      void this.router.navigateByUrl('/mobile/auth/sign-in');
    });
  }

  signOutEverywhere(): void {
    if (this.signingOut()) {
      return;
    }
    this.signingOut.set(true);
    this.api.logoutAll().subscribe(() => {
      this.session.clear();
      void this.router.navigateByUrl('/mobile/auth/sign-in');
    });
  }
}
