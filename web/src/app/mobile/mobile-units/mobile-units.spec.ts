import { TestBed } from '@angular/core/testing';

import { ROSTER } from '../../board-preview/board-preview.fixtures';
import { MobileUnits } from './mobile-units';

describe('MobileUnits', () => {
  it('renders the exact approved roster order with text qualifiers', () => {
    TestBed.configureTestingModule({ imports: [MobileUnits] });
    const fixture = TestBed.createComponent(MobileUnits);
    fixture.detectChanges();
    const el = fixture.nativeElement as HTMLElement;
    const ids = Array.from(el.querySelectorAll('.unit-id')).map((node) => node.textContent?.trim());
    expect(ids).toEqual(['DC', 'E2', 'E3', 'E4', 'E5', 'SQ1', 'SQ8', 'T1']);
    expect(ids).toEqual(ROSTER.map((unit) => unit.id));
    expect(el.querySelectorAll('.unit-status').length).toBe(8);
    expect(el.querySelectorAll('.unit-qualifier').length).toBe(8);
    expect(el.textContent).toContain('not current apparatus state');
    expect(el.textContent).toContain('synthetic display only');
    fixture.destroy();
  });

  it('keeps out-of-service content synthetic and non-operational', () => {
    TestBed.configureTestingModule({ imports: [MobileUnits] });
    const fixture = TestBed.createComponent(MobileUnits);
    fixture.detectChanges();
    const text = (fixture.nativeElement as HTMLElement).textContent ?? '';
    expect(text).toContain('Synthetic out-of-service preview');
    expect(text).toContain('No controls and no CAD authority');
    fixture.destroy();
  });
});
