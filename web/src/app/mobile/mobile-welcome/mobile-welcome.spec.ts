import { TestBed } from '@angular/core/testing';
import { Router } from '@angular/router';

import { MobileWelcome } from './mobile-welcome';

describe('MobileWelcome', () => {
  const navigateByUrl = vi.fn<() => Promise<boolean>>();

  beforeEach(() => {
    navigateByUrl.mockReset();
    navigateByUrl.mockResolvedValue(true);
    TestBed.configureTestingModule({
      imports: [MobileWelcome],
      providers: [{ provide: Router, useValue: { navigateByUrl } }],
    });
  });

  it('clearly labels prototype access without credential controls', () => {
    const fixture = TestBed.createComponent(MobileWelcome);
    fixture.detectChanges();
    const el = fixture.nativeElement as HTMLElement;
    expect(el.textContent).toContain('Prototype access — not authentication.');
    expect(el.textContent).toContain('No credentials are collected or verified.');
    expect(el.querySelector('form')).toBeNull();
    expect(el.querySelector('input')).toBeNull();
    expect(el.querySelector('textarea')).toBeNull();
    fixture.destroy();
  });

  it('enters by navigation only and writes no browser storage', () => {
    const storageWrite = vi.spyOn(Storage.prototype, 'setItem');
    const fixture = TestBed.createComponent(MobileWelcome);
    fixture.detectChanges();
    const button = (fixture.nativeElement as HTMLElement).querySelector('button');
    button?.click();

    expect(navigateByUrl).toHaveBeenCalledExactlyOnceWith('/mobile/home');
    expect(storageWrite).not.toHaveBeenCalled();
    storageWrite.mockRestore();
    fixture.destroy();
  });
});
