import { TestBed } from '@angular/core/testing';
import { provideRouter } from '@angular/router';

import { ACTIVE_INCIDENT, PENDING } from '../../board-preview/board-preview.fixtures';
import { MobileHome } from './mobile-home';

describe('MobileHome', () => {
  it('shows exactly the compact pending and active synthetic summaries', () => {
    TestBed.configureTestingModule({ imports: [MobileHome], providers: [provideRouter([])] });
    const fixture = TestBed.createComponent(MobileHome);
    fixture.detectChanges();
    const el = fixture.nativeElement as HTMLElement;
    const cards = el.querySelectorAll('.summary-card');
    expect(cards.length).toBe(2);
    expect(el.textContent).toContain(PENDING.reference);
    expect(el.textContent).toContain(ACTIVE_INCIDENT.reference);
    expect(el.textContent).toContain('100 TEST STREET');
    expect(el.textContent).toContain('evidence only');
    expect(el.textContent).toContain('not proof of one incident');
    fixture.destroy();
  });
});
