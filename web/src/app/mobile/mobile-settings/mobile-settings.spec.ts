import { TestBed } from '@angular/core/testing';
import { Router } from '@angular/router';

import { MOBILE_BUILD_INFO } from '../mobile-build-info';
import { MobileSettings } from './mobile-settings';

describe('MobileSettings', () => {
  const navigateByUrl = vi.fn<() => Promise<boolean>>();

  beforeEach(() => {
    navigateByUrl.mockReset();
    navigateByUrl.mockResolvedValue(true);
    TestBed.configureTestingModule({
      imports: [MobileSettings],
      providers: [{ provide: Router, useValue: { navigateByUrl } }],
    });
  });

  it('labels navigator connectivity as a browser hint only', () => {
    const fixture = TestBed.createComponent(MobileSettings);
    fixture.detectChanges();
    window.dispatchEvent(new Event('offline'));
    fixture.detectChanges();
    const text = (fixture.nativeElement as HTMLElement).textContent ?? '';
    expect(text).toContain('Browser connectivity hint: Offline');
    expect(text).toContain('does not prove CAD, radio, API, or server connectivity or freshness');
    window.dispatchEvent(new Event('online'));
    fixture.destroy();
  });

  it('shows static build information without a runtime endpoint', () => {
    const fixture = TestBed.createComponent(MobileSettings);
    fixture.detectChanges();
    const text = (fixture.nativeElement as HTMLElement).textContent ?? '';
    expect(text).toContain(MOBILE_BUILD_INFO.applicationVersion);
    expect(text).toContain(MOBILE_BUILD_INFO.buildIdentifier);
    expect(text).toContain(MOBILE_BUILD_INFO.buildDate);
    expect(text).toContain('No version endpoint is contacted');
    fixture.destroy();
  });

  it('logs out by navigation only and leaves browser storage untouched', () => {
    const storageWrite = vi.spyOn(Storage.prototype, 'setItem');
    const storageRemove = vi.spyOn(Storage.prototype, 'removeItem');
    const storageClear = vi.spyOn(Storage.prototype, 'clear');
    const fixture = TestBed.createComponent(MobileSettings);
    fixture.detectChanges();
    const el = fixture.nativeElement as HTMLElement;
    expect(el.textContent).toContain('Prototype only — no authenticated session exists.');

    el.querySelector<HTMLButtonElement>('button')?.click();

    expect(navigateByUrl).toHaveBeenCalledExactlyOnceWith('/mobile/welcome');
    expect(storageWrite).not.toHaveBeenCalled();
    expect(storageRemove).not.toHaveBeenCalled();
    expect(storageClear).not.toHaveBeenCalled();
    storageWrite.mockRestore();
    storageRemove.mockRestore();
    storageClear.mockRestore();
    fixture.destroy();
  });
});
