# Web

This project was generated using [Angular CLI](https://github.com/angular/angular-cli) version 22.1.7.

## Development server

To start a local development server, run:

```bash
ng serve
```

Once the server is running, open your browser and navigate to `http://localhost:4200/`. The application will automatically reload whenever you modify any of the source files.

## Code scaffolding

Angular CLI includes powerful code scaffolding tools. To generate a new component, run:

```bash
ng generate component component-name
```

For a complete list of available schematics (such as `components`, `directives`, or `pipes`), run:

```bash
ng generate --help
```

## Building

To build the project run:

```bash
ng build
```

This will compile your project and store the build artifacts in the `dist/` directory. By default, the production build optimizes your application for performance and speed.

## Running unit tests

To execute unit tests with the [Vitest](https://vitest.dev/) test runner, use the following command:

```bash
ng test
```

## Manual mobile PWA acceptance

PWA-16 and PWA-17 in the [mobile/PWA beta contract](../docs/mobile-pwa-beta-v1.md)
require a separately authorized HTTPS preview deployment and physical-device testing. No deployment
is part of Step 7B.

- Android Chrome: install or Add to Home Screen, open in standalone mode, confirm both SHADOW /
  REPLAY warnings, then confirm the cached synthetic shell shows the approved offline message after a
  successful online load.
- iPhone Safari: use Share → Add to Home Screen and open in standalone mode. Confirm the installed
  name is `GFR V3 SHADOW`, confirm the icon and both warnings, then test the cached synthetic shell
  offline. Safari versions before 16.4 may install only a bookmark and may not provide full
  service-worker offline behavior.
- On both platforms, publish a newer synthetic hashed build only under a later deployment
  authorization; confirm the stale-preview message appears and that activation/reload occurs only
  after tapping `Reload synthetic preview`.

The PWA remains a public synthetic preview. Add to Home Screen, standalone display, service-worker
caching, and HTTPS do not authenticate GFD or CAD and do not authorize emergency use.

## Running end-to-end tests

For end-to-end (e2e) testing, run:

```bash
ng e2e
```

Angular CLI does not come with an end-to-end testing framework by default. You can choose one that suits your needs.

## Additional Resources

For more information on using the Angular CLI, including detailed command references, visit the [Angular CLI Overview and Command Reference](https://angular.dev/tools/cli) page.
