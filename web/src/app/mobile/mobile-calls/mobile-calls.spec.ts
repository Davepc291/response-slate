import { TestBed } from '@angular/core/testing';

import { MobileCalls } from './mobile-calls';

describe('MobileCalls', () => {
  it('shows only the existing synthetic pending and active call details', () => {
    TestBed.configureTestingModule({ imports: [MobileCalls] });
    const fixture = TestBed.createComponent(MobileCalls);
    fixture.detectChanges();
    const text = (fixture.nativeElement as HTMLElement).textContent ?? '';
    expect(text).toContain('SYN-PENDING-01');
    expect(text).toContain('SYN-INCIDENT-01');
    expect(text).toContain('100 TEST STREET');
    expect(text).toContain('Call type');
    expect(text).toContain('unresolved');
    expect(text).toContain('Channel and TGID are evidence only');
    expect(text).toContain('No call history, live feed, or operational actions.');
    fixture.destroy();
  });
});
