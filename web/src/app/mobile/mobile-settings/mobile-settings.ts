import { Component, DestroyRef, inject, signal } from '@angular/core';
import { Router } from '@angular/router';

import { MOBILE_BUILD_INFO } from '../mobile-build-info';

@Component({
  selector: 'app-mobile-settings',
  templateUrl: './mobile-settings.html',
  styleUrl: './mobile-settings.scss',
})
export class MobileSettings {
  private readonly destroyRef = inject(DestroyRef);
  private readonly router = inject(Router);

  readonly online = signal(navigator.onLine);
  readonly buildInfo = MOBILE_BUILD_INFO;

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

  leavePrototype(): void {
    void this.router.navigateByUrl('/mobile/welcome');
  }
}
