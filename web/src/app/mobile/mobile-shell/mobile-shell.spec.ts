import { TestBed } from '@angular/core/testing';
import { provideRouter } from '@angular/router';

import { OFFLINE_MESSAGE } from '../../pwa-status/pwa-status';
import { MobileShell, MOBILE_LOADING_MESSAGE } from './mobile-shell';

describe('MobileShell', () => {
  beforeEach(() => {
    TestBed.configureTestingModule({
      imports: [MobileShell],
      providers: [provideRouter([])],
    });
  });

  function render() {
    const fixture = TestBed.createComponent(MobileShell);
    fixture.detectChanges();
    return { fixture, el: fixture.nativeElement as HTMLElement };
  }

  it('keeps both exact warnings visible in the distinct mobile shell', () => {
    const { fixture, el } = render();
    expect(el.textContent).toContain('SHADOW / REPLAY — NOT LIVE CAD');
    expect(el.textContent).toContain('Synthetic replay preview. No operational authority.');
    expect(el.querySelector('.shadow-board')).toBeNull();
    expect(el.querySelector('.mobile-app')).not.toBeNull();
    fixture.destroy();
  });

  it('renders five labeled navigation targets in the approved order', () => {
    const { fixture, el } = render();
    const links = Array.from(el.querySelectorAll('.mobile-navigation a'));
    expect(links.map((link) => link.textContent?.trim())).toEqual([
      '⌂Home',
      '≡Calls',
      '●Units',
      '◇Evidence',
      '⚙Settings',
    ]);
    expect(links.map((link) => link.getAttribute('href'))).toEqual([
      '/mobile/home',
      '/mobile/calls',
      '/mobile/units',
      '/mobile/evidence',
      '/mobile/settings',
    ]);
    fixture.destroy();
  });

  it('shows approved loading copy only while route rendering is pending', () => {
    const { fixture, el } = render();
    expect(el.textContent).not.toContain(MOBILE_LOADING_MESSAGE);
    fixture.componentInstance.loading.set(true);
    fixture.detectChanges();
    expect(el.textContent).toContain(MOBILE_LOADING_MESSAGE);
    fixture.destroy();
  });

  it('keeps both warnings visible with the existing offline state', () => {
    const { fixture, el } = render();
    window.dispatchEvent(new Event('offline'));
    fixture.detectChanges();
    expect(el.textContent).toContain('SHADOW / REPLAY — NOT LIVE CAD');
    expect(el.textContent).toContain('Synthetic replay preview. No operational authority.');
    expect(el.textContent).toContain(OFFLINE_MESSAGE);
    window.dispatchEvent(new Event('online'));
    fixture.destroy();
  });
});
