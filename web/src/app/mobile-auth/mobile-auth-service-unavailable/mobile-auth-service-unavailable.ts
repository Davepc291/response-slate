import { Component, inject } from '@angular/core';
import { Router } from '@angular/router';

import { HEADER_TITLE } from '../../board-preview/board-preview.fixtures';

/**
 * Explicit service-unavailable screen at /mobile/auth/service-unavailable
 * (Step 9D requirement 1/4). Reached directly, or by navigation from any
 * other auth screen when the backend is unreachable. It never exposes a
 * raw error, status code, or backend detail — only the exact contract-
 * approved copy and a retry path back to sign-in.
 */
@Component({
  selector: 'app-mobile-auth-service-unavailable',
  templateUrl: './mobile-auth-service-unavailable.html',
  styleUrl: './mobile-auth-service-unavailable.scss',
})
export class MobileAuthServiceUnavailable {
  private readonly router = inject(Router);

  readonly headerTitle = HEADER_TITLE;

  retry(): void {
    void this.router.navigateByUrl('/mobile/auth/sign-in');
  }
}
