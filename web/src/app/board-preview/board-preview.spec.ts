import { TestBed } from '@angular/core/testing';

import { OFFLINE_MESSAGE } from '../pwa-status/pwa-status';
import { BoardPreview } from './board-preview';
import { ROSTER, SHADOW_WARNING, SYNTHETIC_WARNING } from './board-preview.fixtures';

describe('BoardPreview', () => {
  beforeEach(async () => {
    await TestBed.configureTestingModule({
      imports: [BoardPreview],
    }).compileComponents();
  });

  function render() {
    const fixture = TestBed.createComponent(BoardPreview);
    fixture.detectChanges();
    return { fixture, el: fixture.nativeElement as HTMLElement };
  }

  afterEach(() => {
    TestBed.resetTestingModule();
  });

  it('shows the shadow/replay warning', () => {
    const { fixture, el } = render();
    expect(el.textContent).toContain(SHADOW_WARNING);
    fixture.destroy();
  });

  it('shows the non-operational synthetic warning', () => {
    const { fixture, el } = render();
    const text = el.textContent ?? '';
    expect(text).toContain(SYNTHETIC_WARNING);
    expect(text).toContain('No operational authority');
    fixture.destroy();
  });

  it('keeps both warnings visible with the offline synthetic-shell message', () => {
    const { fixture, el } = render();
    window.dispatchEvent(new Event('offline'));
    fixture.detectChanges();
    const text = el.textContent ?? '';
    expect(text).toContain(SHADOW_WARNING);
    expect(text).toContain(SYNTHETIC_WARNING);
    expect(text).toContain(OFFLINE_MESSAGE);
    window.dispatchEvent(new Event('online'));
    fixture.destroy();
  });

  it('renders the exact primary roster order', () => {
    const { fixture, el } = render();
    const ids = Array.from(el.querySelectorAll('.unit-id')).map((node) => node.textContent?.trim());
    expect(ids).toEqual(['DC', 'E2', 'E3', 'E4', 'E5', 'SQ1', 'SQ8', 'T1']);
    expect(ROSTER.map((unit) => unit.id)).toEqual(ids);
    fixture.destroy();
  });

  it('labels channel and TGID as evidence only', () => {
    const { fixture, el } = render();
    const text = el.textContent ?? '';
    expect(text).toContain('evidence only');
    expect(text).toContain('CH1A');
    expect(text).toContain('CH2B');
    expect(text).toContain('CH3B');
    expect(text).toContain('CH4C');
    expect(text).toContain('Channel and TGID are evidence only');
    fixture.destroy();
  });

  it('labels fixtures as synthetic', () => {
    const { fixture, el } = render();
    const text = el.textContent ?? '';
    expect(text).toContain('SYN-PENDING-01');
    expect(text).toContain('SYN-INCIDENT-01');
    expect(text).toContain('100 TEST STREET');
    expect(text).toContain('SYNTHETIC RAW MODEL');
    expect(text).toContain('SYNTHETIC HUMAN REFERENCE');
    expect(text).toContain('Synthetic out-of-service preview');
    fixture.destroy();
  });

  it('keeps unresolved, ambiguous, rejected, and unbound evidence visible', () => {
    const { fixture, el } = render();
    const text = el.textContent ?? '';
    expect(text).toContain('Call type: unresolved');
    expect(text).toContain('Ambiguous unit evidence');
    expect(text).toContain('Rejected unit evidence');
    expect(text).toContain('Unbound status association');
    expect(text).toContain('unsupported_context');
    fixture.destroy();
  });

  it('does not claim live CAD, one-incident proof, or current apparatus state', () => {
    const { fixture, el } = render();
    const text = el.textContent ?? '';
    expect(text).toContain('NOT LIVE CAD');
    expect(text).not.toMatch(/official CAD/i);
    expect(text).not.toMatch(/production CAD/i);
    expect(text).not.toMatch(/live incident/i);
    expect(text).toContain('not proof of one incident');
    expect(text).not.toMatch(/\bboth_resolved\b/);
    expect(text).toContain('not current apparatus state');
    expect(text).not.toMatch(/is current apparatus state/i);
    expect(text).not.toMatch(/current unit status/i);
    fixture.destroy();
  });
});
