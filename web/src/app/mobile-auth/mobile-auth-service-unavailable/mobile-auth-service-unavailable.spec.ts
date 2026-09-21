import { TestBed } from '@angular/core/testing';
import { Router } from '@angular/router';

import { MobileAuthServiceUnavailable } from './mobile-auth-service-unavailable';

describe('MobileAuthServiceUnavailable', () => {
  let navigateByUrl: ReturnType<typeof vi.fn<() => Promise<boolean>>>;

  beforeEach(() => {
    navigateByUrl = vi.fn().mockResolvedValue(true);
    TestBed.configureTestingModule({
      imports: [MobileAuthServiceUnavailable],
      providers: [{ provide: Router, useValue: { navigateByUrl } }],
    });
  });

  it('shows the exact contract-approved unavailable copy with no technical detail', () => {
    const fixture = TestBed.createComponent(MobileAuthServiceUnavailable);
    fixture.detectChanges();
    const el = fixture.nativeElement as HTMLElement;
    expect(el.textContent).toContain('Sign-in is temporarily unavailable. NOT LIVE CAD.');
    expect(el.textContent).not.toMatch(/stack trace|exception|sql|http 5\d\d/i);
    fixture.destroy();
  });

  it('offers a clear retry that returns to sign-in', () => {
    const fixture = TestBed.createComponent(MobileAuthServiceUnavailable);
    fixture.detectChanges();
    (fixture.nativeElement as HTMLElement).querySelector('button')?.click();
    expect(navigateByUrl).toHaveBeenCalledExactlyOnceWith('/mobile/auth/sign-in');
    fixture.destroy();
  });
});
