import { DOCUMENT } from '@angular/common';
import { Component, DestroyRef, inject, signal } from '@angular/core';
import { takeUntilDestroyed } from '@angular/core/rxjs-interop';
import {
  NavigationCancel,
  NavigationEnd,
  NavigationError,
  NavigationStart,
  Router,
  RouterOutlet,
} from '@angular/router';

export const AUTH_LOADING_MESSAGE = 'Loading authentication preview. NOT LIVE CAD.';

/**
 * Layout shell for the production /mobile/auth/** routes. It deliberately
 * does not reuse MobileShell: this surface performs real authentication
 * requests against the live API and must not carry MobileShell's "Mobile
 * synthetic preview" framing, bottom navigation, or SHADOW/REPLAY and
 * synthetic-preview warnings — those describe the non-authenticating
 * synthetic board/mobile experience (see BoardPreview and MobileShell),
 * which this shell is not. See docs/authentication-authorization-v1.md
 * Section 3 for the production login screen's contract.
 */
@Component({
  selector: 'app-mobile-auth-shell',
  imports: [RouterOutlet],
  templateUrl: './mobile-auth-shell.html',
  styleUrl: './mobile-auth-shell.scss',
})
export class MobileAuthShell {
  private readonly destroyRef = inject(DestroyRef);
  private readonly document = inject(DOCUMENT);
  private readonly router = inject(Router);

  readonly loadingMessage = AUTH_LOADING_MESSAGE;
  readonly loading = signal(false);

  constructor() {
    this.router.events.pipe(takeUntilDestroyed(this.destroyRef)).subscribe((event) => {
      if (event instanceof NavigationStart) {
        this.loading.set(true);
      } else if (event instanceof NavigationEnd) {
        this.loading.set(false);
        queueMicrotask(() => {
          this.document
            .querySelector<HTMLElement>('.mobile-auth-main h1')
            ?.focus({ preventScroll: true });
        });
      } else if (event instanceof NavigationCancel || event instanceof NavigationError) {
        this.loading.set(false);
      }
    });
  }
}
