import { DOCUMENT } from '@angular/common';
import { Component, DestroyRef, computed, inject, signal } from '@angular/core';
import { takeUntilDestroyed } from '@angular/core/rxjs-interop';
import {
  NavigationCancel,
  NavigationEnd,
  NavigationError,
  NavigationStart,
  Router,
  RouterLink,
  RouterLinkActive,
  RouterOutlet,
} from '@angular/router';

import {
  HEADER_TITLE,
  SHADOW_WARNING,
  SYNTHETIC_WARNING,
} from '../../board-preview/board-preview.fixtures';
import { PwaStatus } from '../../pwa-status/pwa-status';

export const MOBILE_LOADING_MESSAGE = 'Loading synthetic mobile preview. NOT LIVE CAD.';

@Component({
  selector: 'app-mobile-shell',
  imports: [PwaStatus, RouterLink, RouterLinkActive, RouterOutlet],
  templateUrl: './mobile-shell.html',
  styleUrl: './mobile-shell.scss',
})
export class MobileShell {
  private readonly destroyRef = inject(DestroyRef);
  private readonly document = inject(DOCUMENT);
  private readonly router = inject(Router);

  readonly headerTitle = HEADER_TITLE;
  readonly shadowWarning = SHADOW_WARNING;
  readonly syntheticWarning = SYNTHETIC_WARNING;
  readonly loadingMessage = MOBILE_LOADING_MESSAGE;
  readonly loading = signal(false);
  readonly currentUrl = signal(this.router.url);
  readonly showNavigation = computed(() => !this.currentUrl().endsWith('/welcome'));

  constructor() {
    this.router.events.pipe(takeUntilDestroyed(this.destroyRef)).subscribe((event) => {
      if (event instanceof NavigationStart) {
        this.loading.set(true);
      } else if (event instanceof NavigationEnd) {
        this.loading.set(false);
        this.currentUrl.set(event.urlAfterRedirects);
        queueMicrotask(() => {
          this.document
            .querySelector<HTMLElement>('.mobile-main h1')
            ?.focus({ preventScroll: true });
        });
      } else if (event instanceof NavigationCancel || event instanceof NavigationError) {
        this.loading.set(false);
      }
    });
  }
}
