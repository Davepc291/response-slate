import { Component, DestroyRef, inject, signal } from '@angular/core';
import { Router, RouterLink } from '@angular/router';

import { AuthApiService } from '../../mobile-auth/auth-data/auth-api.service';
import { AuthSessionState } from '../../mobile-auth/auth-data/auth-session-state.service';
import { MOBILE_BUILD_INFO } from '../mobile-build-info';

/**
 * This screen sits behind requireVerifiedSession (see mobile.routes.ts), so
 * its Account section is a real, authenticated sign-out: it goes through the
 * exact same AuthApiService.logout()/AuthSessionState.clear() flow as
 * mobile-auth-sessions.ts's own "Log out", never a second mechanism, and
 * never clears cookies itself — the server response does that. "Log out
 * everywhere" stays off this screen and only lives on /mobile/auth/sessions,
 * matching that screen's own destructive-action placement.
 */
@Component({
  selector: 'app-mobile-settings',
  imports: [RouterLink],
  templateUrl: './mobile-settings.html',
  styleUrl: './mobile-settings.scss',
})
export class MobileSettings {
  private readonly destroyRef = inject(DestroyRef);
  private readonly router = inject(Router);
  private readonly api = inject(AuthApiService);
  private readonly session = inject(AuthSessionState);

  readonly online = signal(navigator.onLine);
  readonly buildInfo = MOBILE_BUILD_INFO;
  readonly loggingOut = signal(false);
  readonly logoutError = signal<string | null>(null);

  constructor() {
    const markOnline = () => this.online.set(true);
    const markOffline = () => this.online.set(false);
    window.addEventListener('online', markOnline);
    window.addEventListener('offline', markOffline);
    this.destroyRef.onDestroy(() => {
      window.removeEventListener('online', markOnline);
      window.removeEventListener('offline', markOffline);
    });
  }

  logOut(): void {
    if (this.loggingOut()) {
      return;
    }
    this.loggingOut.set(true);
    this.logoutError.set(null);
    this.api.logout().subscribe((result) => {
      this.loggingOut.set(false);
      if (!result.ok) {
        // A failed server-side logout must never present as a successful
        // sign-out: the session may still be fully valid, so clearing local
        // state or navigating to sign-in here would falsely tell the user
        // they are signed out.
        this.logoutError.set('Unable to log out right now. Try again.');
        return;
      }
      this.session.clear();
      void this.router.navigateByUrl('/mobile/auth/sign-in');
    });
  }
}
