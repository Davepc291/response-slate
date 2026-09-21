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

  it('shows both exact required warnings and a distinguishing real-auth label', () => {
    const fixture = TestBed.createComponent(MobileAuthShell);
    fixture.detectChanges();
    const el = fixture.nativeElement as HTMLElement;

    expect(el.textContent).toContain(SHADOW_WARNING);
    expect(el.textContent).toContain(SYNTHETIC_WARNING);
    expect(el.textContent).toContain('Greenwich Fire Responder V3');
    // Distinguishes itself from MobileShell's synthetic-preview framing.
    expect(el.textContent).not.toContain('Mobile synthetic preview');
    expect(el.textContent).not.toContain('Prototype access — not authentication.');
    fixture.destroy();
  });

  it('renders one main landmark for the routed content', () => {
    const fixture = TestBed.createComponent(MobileAuthShell);
    fixture.detectChanges();
    const el = fixture.nativeElement as HTMLElement;
    expect(el.querySelectorAll('main').length).toBe(1);
    fixture.destroy();
  });
});
