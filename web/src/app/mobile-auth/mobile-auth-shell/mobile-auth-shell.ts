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

import {
  HEADER_TITLE,
  SHADOW_WARNING,
  SYNTHETIC_WARNING,
} from '../../board-preview/board-preview.fixtures';

export const AUTH_LOADING_MESSAGE = 'Loading authentication preview. NOT LIVE CAD.';
export const AUTH_PREVIEW_LABEL =
  'Real authentication preview — connects to the authentication service, not yet the production sign-in.';

/**
 * Layout shell for the /mobile/auth/** preview routes. It deliberately does
 * not reuse MobileShell: this surface performs real authentication requests
 * against the live Step 9C API, and Step 9D requirement 10 ("clearly
 * distinguish the real authorization UI from the existing synthetic
 * preview... do not label authentication as synthetic") means it must not
 * share MobileShell's "Mobile synthetic preview" framing or its bottom
 * navigation. Both contract-mandated warnings still appear, unchanged, on
 * every state, per docs/authentication-authorization-v1.md Section 3's
 * proposed login-screen text — those warnings describe the overall
 * application's non-operational status, not the authenticity of sign-in.
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

  readonly headerTitle = HEADER_TITLE;
  readonly shadowWarning = SHADOW_WARNING;
  readonly syntheticWarning = SYNTHETIC_WARNING;
  readonly previewLabel = AUTH_PREVIEW_LABEL;
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
