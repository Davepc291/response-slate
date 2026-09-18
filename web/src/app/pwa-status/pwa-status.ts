import { Component, DestroyRef, InjectionToken, computed, inject, signal } from '@angular/core';
import { takeUntilDestroyed } from '@angular/core/rxjs-interop';
import { SwUpdate } from '@angular/service-worker';

export const OFFLINE_MESSAGE = 'Offline. Synthetic replay preview only. NOT LIVE CAD.';
export const UNAVAILABLE_MESSAGE =
  'Preview unavailable. Synthetic board is not loaded. NOT LIVE CAD.';
export const STALE_MESSAGE = 'Stale preview. A newer synthetic build is available. NOT LIVE CAD.';

export const PWA_RELOAD = new InjectionToken<() => void>('PWA_RELOAD', {
  providedIn: 'root',
  factory: () => () => window.location.reload(),
});

@Component({
  selector: 'app-pwa-status',
  templateUrl: './pwa-status.html',
  styleUrl: './pwa-status.scss',
})
export class PwaStatus {
  private readonly destroyRef = inject(DestroyRef);
  private readonly swUpdate = inject(SwUpdate, { optional: true });
  private readonly reloadPage = inject(PWA_RELOAD);
  private readonly online = signal(navigator.onLine);
  private readonly stale = signal(false);
  private readonly unavailable = signal(false);

  readonly activating = signal(false);
  readonly state = computed<'offline' | 'unavailable' | 'stale' | null>(() => {
    if (!this.online()) {
      return 'offline';
    }
    if (this.stale()) {
      return 'stale';
    }
    if (this.unavailable()) {
      return 'unavailable';
    }
    return null;
  });
  readonly message = computed(() => {
    switch (this.state()) {
      case 'offline':
        return OFFLINE_MESSAGE;
      case 'unavailable':
        return UNAVAILABLE_MESSAGE;
      case 'stale':
        return STALE_MESSAGE;
      default:
        return null;
    }
  });

  constructor() {
    const markOnline = () => this.online.set(true);
    const markOffline = () => this.online.set(false);
    window.addEventListener('online', markOnline);
    window.addEventListener('offline', markOffline);
    this.destroyRef.onDestroy(() => {
      window.removeEventListener('online', markOnline);
      window.removeEventListener('offline', markOffline);
    });

    if (!this.swUpdate?.isEnabled) {
      return;
    }

    this.swUpdate.versionUpdates.pipe(takeUntilDestroyed(this.destroyRef)).subscribe((event) => {
      if (event.type === 'VERSION_READY') {
        this.stale.set(true);
      } else if (event.type === 'VERSION_INSTALLATION_FAILED') {
        this.unavailable.set(true);
      }
    });
    this.swUpdate.unrecoverable.pipe(takeUntilDestroyed(this.destroyRef)).subscribe(() => {
      this.unavailable.set(true);
    });
  }

  async reloadSyntheticPreview(): Promise<void> {
    if (!this.swUpdate || this.activating()) {
      return;
    }

    this.activating.set(true);
    try {
      await this.swUpdate.activateUpdate();
      this.reloadPage();
    } catch {
      this.unavailable.set(true);
      this.stale.set(false);
      this.activating.set(false);
    }
  }
}
