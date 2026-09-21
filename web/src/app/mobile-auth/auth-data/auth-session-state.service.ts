import { Injectable, computed, inject, signal } from '@angular/core';

import { MeResponse } from './auth-api.models';
import { AuthApiService } from './auth-api.service';

export type SessionStatus =
  'checking' | 'authenticated' | 'unauthenticated' | 'unavailable' | 'expired';

/**
 * Minimal in-memory session-state foundation for a future Step 9F
 * navigation guard. The source of truth is always GET /api/auth/me on the
 * backend — this service never treats its own cached state as
 * authorization, and it never persists to localStorage, sessionStorage, or
 * IndexedDB, so a page refresh always returns to `checking` and re-verifies
 * against the server rather than trusting stale client state.
 */
@Injectable({ providedIn: 'root' })
export class AuthSessionState {
  private readonly api = inject(AuthApiService);

  private readonly statusSignal = signal<SessionStatus>('checking');
  private readonly accountSignal = signal<MeResponse | null>(null);

  readonly status = this.statusSignal.asReadonly();
  readonly account = this.accountSignal.asReadonly();
  readonly isAuthenticated = computed(() => this.statusSignal() === 'authenticated');

  /** Re-checks GET /api/auth/me and updates state from the server's answer. */
  refresh(): void {
    this.statusSignal.set('checking');
    this.api.me().subscribe((result) => {
      if (result.ok) {
        this.accountSignal.set(result.value);
        this.statusSignal.set('authenticated');
        return;
      }
      this.accountSignal.set(null);
      if (result.error.kind === 'session_expired') {
        this.statusSignal.set('expired');
      } else if (result.error.kind === 'not_authenticated') {
        this.statusSignal.set('unauthenticated');
      } else {
        this.statusSignal.set('unavailable');
      }
    });
  }

  /** Records a session already verified by a caller's own /api/auth/me call. */
  setAuthenticated(account: MeResponse): void {
    this.accountSignal.set(account);
    this.statusSignal.set('authenticated');
  }

  clear(): void {
    this.accountSignal.set(null);
    this.statusSignal.set('unauthenticated');
  }
}
