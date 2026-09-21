import { TestBed } from '@angular/core/testing';
import { provideRouter } from '@angular/router';

import { MobileAuthAdminAccessDenied } from './mobile-auth-admin-access-denied';

describe('MobileAuthAdminAccessDenied', () => {
  it('renders a safe, generic access-denied message with no user data', () => {
    TestBed.configureTestingModule({
      imports: [MobileAuthAdminAccessDenied],
      providers: [provideRouter([])],
    });
    const fixture = TestBed.createComponent(MobileAuthAdminAccessDenied);
    fixture.detectChanges();
    const el = fixture.nativeElement as HTMLElement;
    expect(el.querySelector('h1')?.textContent).toContain('Access denied');
    expect(el.textContent).toContain('do not have permission');
    fixture.destroy();
  });
});
