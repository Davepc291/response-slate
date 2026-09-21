import { provideHttpClient, withInterceptors } from '@angular/common/http';
import { ApplicationConfig, isDevMode, provideBrowserGlobalErrorListeners } from '@angular/core';
import { provideRouter } from '@angular/router';
import { provideServiceWorker } from '@angular/service-worker';
import { routes } from './app.routes';
import { csrfInterceptor } from './mobile-auth/auth-data/csrf.interceptor';

export const appConfig: ApplicationConfig = {
  providers: [
    provideBrowserGlobalErrorListeners(),
    provideRouter(routes),
    // Used only by the Step 9D /mobile/auth/** authentication client
    // (mobile-auth/auth-data/auth-api.service.ts). No other application code
    // issues HTTP requests; the synthetic board/mobile preview remains a
    // pure static/fixture surface.
    provideHttpClient(withInterceptors([csrfInterceptor])),
    provideServiceWorker('ngsw-worker.js', {
      enabled: !isDevMode(),
      registrationStrategy: 'registerWhenStable:30000',
    }),
  ],
};
