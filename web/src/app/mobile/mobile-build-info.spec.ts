import { MOBILE_BUILD_INFO } from './mobile-build-info';

describe('MOBILE_BUILD_INFO', () => {
  it('contains only reviewed compile-time display values', () => {
    expect(MOBILE_BUILD_INFO).toEqual({
      applicationVersion: 'mobile-phone-experience-v1',
      buildIdentifier: 'step-7d',
      buildDate: '2026-09-19',
    });
    expect(Object.keys(MOBILE_BUILD_INFO)).toEqual([
      'applicationVersion',
      'buildIdentifier',
      'buildDate',
    ]);
  });
});
