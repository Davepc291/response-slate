import { TestBed } from '@angular/core/testing';
import { SwUpdate } from '@angular/service-worker';
import { Subject } from 'rxjs';

import {
  OFFLINE_MESSAGE,
  PWA_RELOAD,
  PwaStatus,
  STALE_MESSAGE,
  UNAVAILABLE_MESSAGE,
} from './pwa-status';

describe('PwaStatus', () => {
  let versionUpdates: Subject<unknown>;
  let unrecoverable: Subject<unknown>;
  let activateUpdate: ReturnType<typeof vi.fn<() => Promise<boolean>>>;
  let reload: ReturnType<typeof vi.fn>;

  beforeEach(() => {
    versionUpdates = new Subject<unknown>();
    unrecoverable = new Subject<unknown>();
    activateUpdate = vi.fn<() => Promise<boolean>>().mockResolvedValue(true);
    reload = vi.fn();

    TestBed.configureTestingModule({
      imports: [PwaStatus],
      providers: [
        {
          provide: SwUpdate,
          useValue: {
            isEnabled: true,
            versionUpdates,
            unrecoverable,
            activateUpdate,
          },
        },
        { provide: PWA_RELOAD, useValue: reload },
      ],
    });
  });

  function render() {
    const fixture = TestBed.createComponent(PwaStatus);
    fixture.detectChanges();
    return { fixture, el: fixture.nativeElement as HTMLElement };
  }

  it('shows the approved offline message and clears it when connectivity returns', () => {
    const { fixture, el } = render();
    window.dispatchEvent(new Event('offline'));
    fixture.detectChanges();
    expect(el.textContent).toContain(OFFLINE_MESSAGE);

    window.dispatchEvent(new Event('online'));
    fixture.detectChanges();
    expect(el.textContent).not.toContain(OFFLINE_MESSAGE);
    fixture.destroy();
  });

  it('shows the approved stale message without silently activating the update', () => {
    const { fixture, el } = render();
    versionUpdates.next({
      type: 'VERSION_READY',
      currentVersion: { hash: 'old' },
      latestVersion: { hash: 'new' },
    });
    fixture.detectChanges();

    expect(el.textContent).toContain(STALE_MESSAGE);
    expect(el.querySelector('button')?.textContent?.trim()).toBe('Reload synthetic preview');
    expect(activateUpdate).not.toHaveBeenCalled();
    expect(reload).not.toHaveBeenCalled();
    fixture.destroy();
  });

  it('activates and reloads only after the explicit reload action', async () => {
    const { fixture, el } = render();
    versionUpdates.next({
      type: 'VERSION_READY',
      currentVersion: { hash: 'old' },
      latestVersion: { hash: 'new' },
    });
    fixture.detectChanges();

    (el.querySelector('button') as HTMLButtonElement).click();
    await fixture.whenStable();

    expect(activateUpdate).toHaveBeenCalledOnce();
    expect(reload).toHaveBeenCalledOnce();
    fixture.destroy();
  });

  it('shows unavailable when update installation fails', () => {
    const { fixture, el } = render();
    versionUpdates.next({
      type: 'VERSION_INSTALLATION_FAILED',
      version: { hash: 'failed' },
      error: 'synthetic test failure',
    });
    fixture.detectChanges();

    expect(el.textContent).toContain(UNAVAILABLE_MESSAGE);
    expect(el.textContent).toContain('NOT LIVE CAD');
    fixture.destroy();
  });

  it('keeps the shell and shows unavailable if explicit activation fails', async () => {
    activateUpdate.mockRejectedValueOnce(new Error('synthetic test failure'));
    const { fixture, el } = render();
    versionUpdates.next({
      type: 'VERSION_READY',
      currentVersion: { hash: 'old' },
      latestVersion: { hash: 'new' },
    });
    fixture.detectChanges();

    (el.querySelector('button') as HTMLButtonElement).click();
    await fixture.whenStable();
    fixture.detectChanges();

    expect(el.textContent).toContain(UNAVAILABLE_MESSAGE);
    expect(reload).not.toHaveBeenCalled();
    fixture.destroy();
  });

  it('shows unavailable for an unrecoverable cached-shell failure', () => {
    const { fixture, el } = render();
    unrecoverable.next({ reason: 'synthetic test failure' });
    fixture.detectChanges();

    expect(el.textContent).toContain(UNAVAILABLE_MESSAGE);
    fixture.destroy();
  });
});
