import { TestBed } from '@angular/core/testing';

import { MobileEvidence } from './mobile-evidence';

describe('MobileEvidence', () => {
  it('keeps every channel and TGID explicitly evidence-only', () => {
    TestBed.configureTestingModule({ imports: [MobileEvidence] });
    const fixture = TestBed.createComponent(MobileEvidence);
    fixture.detectChanges();
    const text = (fixture.nativeElement as HTMLElement).textContent ?? '';
    for (const channel of ['CH1A', 'CH2B', 'CH3B', 'CH4C']) {
      expect(text).toContain(channel);
    }
    expect(text).toContain('evidence labels only');
    expect(text).toContain(
      'Channel and TGID are evidence only. They do not authorize incidents or unit state.',
    );
    fixture.destroy();
  });

  it('preserves synthetic replay, ambiguous, rejected, and unbound caveats', () => {
    TestBed.configureTestingModule({ imports: [MobileEvidence] });
    const fixture = TestBed.createComponent(MobileEvidence);
    fixture.detectChanges();
    const text = (fixture.nativeElement as HTMLElement).textContent ?? '';
    expect(text).toContain('ShadowOnly');
    expect(text).toContain('PairingIntact');
    expect(text).toContain('SYNTHETIC RAW MODEL');
    expect(text).toContain('SYNTHETIC HUMAN REFERENCE');
    expect(text).toContain('Ambiguous unit evidence');
    expect(text).toContain('Rejected unit evidence');
    expect(text).toContain('Unbound status association');
    expect(text).toContain('Neither is operational truth');
    fixture.destroy();
  });
});
