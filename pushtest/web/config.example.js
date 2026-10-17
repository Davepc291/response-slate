// pushtest/web/config.example.js — committed TEMPLATE ONLY. Contains no
// real value of any kind.
//
// Copy this file to config.js (already .gitignore'd — see the repository
// root .gitignore) and fill in the one value locally, never in version
// control:
//
//   vapidPublicKey — the DEV-ONLY VAPID public key from cmd/vapid-keygen.
//       Not a subscription secret (it is designed to be public -- every
//       subscribing browser receives it as applicationServerKey), but
//       still dev-only: never reuse a production key pair here, and none
//       exists yet.
//
// The receiver's bearer token deliberately has NO field here, and never
// should: this file is served as a plain static asset by Caddy to any
// visitor of the test origin, so anything placed here is public the
// moment this is deployed. The token is instead typed by hand into the
// page itself at test time (see index.html's password-type input) and is
// never written to disk, logged, or placed in a URL.
//
// index.html loads this file before its own inline script runs. If
// config.js is missing, the page still loads but the Subscribe button
// reports a clear configuration error instead of silently failing.
window.PUSHTEST_CONFIG = {
  vapidPublicKey: 'REPLACE_WITH_DEV_ONLY_VAPID_PUBLIC_KEY',
};
