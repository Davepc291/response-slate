import { TestBed } from '@angular/core/testing';
import { provideRouter } from '@angular/router';

import { SHADOW_WARNING, SYNTHETIC_WARNING } from '../../board-preview/board-preview.fixtures';
import { MobileAuthShell } from './mobile-auth-shell';

describe('MobileAuthShell', () => {
  beforeEach(() => {
    TestBed.configureTestingModule({
      imports: [MobileAuthShell],
      providers: [provideRouter([])],
    });
  });

  it('does not show the synthetic-preview warnings or a duplicate header logo', () => {
    const fixture = TestBed.createComponent(MobileAuthShell);
    fixture.detectChanges();
    const el = fixture.nativeElement as HTMLElement;

    // This is the production sign-in surface, not the synthetic board/mobile
    // preview: it must not carry SHADOW/REPLAY or synthetic-preview framing,
    // per docs/authentication-authorization-v1.md Section 3.
    expect(el.textContent).not.toContain(SHADOW_WARNING);
    expect(el.textContent).not.toContain(SYNTHETIC_WARNING);
    expect(el.textContent).not.toContain(
      'Real authentication preview — connects to the authentication service, not yet the production sign-in.',
    );
    // Distinguishes itself from MobileShell's synthetic-preview framing.
    expect(el.textContent).not.toContain('Mobile synthetic preview');
    expect(el.textContent).not.toContain('Prototype access — not authentication.');
    // No duplicate header logo — the large logo lives on the sign-in screen itself.
    expect(el.querySelector('.mobile-auth-logo')).toBeNull();
    expect(el.querySelector('header')).toBeNull();
  });

  it('renders one main landmark for the routed content', () => {
    const fixture = TestBed.createComponent(MobileAuthShell);
    fixture.detectChanges();
    const el = fixture.nativeElement as HTMLElement;
    expect(el.querySelectorAll('main').length).toBe(1);
    fixture.destroy();
  });
});
