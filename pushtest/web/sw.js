// pushtest service worker — SANDBOX, NOT the Greenwich Fire Responder app.
//
// Scope is deliberately this page's own origin only (pushtest.*, never
// app.gfrapp.com): this file is served from, and only ever registered by,
// the isolated test origin's own index.html. It never imports, shares a
// cache namespace with, or interferes with the real app's own
// ngsw-worker.js.
//
// This handler never logs the push payload, and the payload itself is
// never the real alert content for any real incident -- only whatever
// title/body a controlled, synthetic sandbox-push invocation sent.

self.addEventListener('install', () => {
  self.skipWaiting();
});

self.addEventListener('activate', (event) => {
  event.waitUntil(self.clients.claim());
});

self.addEventListener('push', (event) => {
  let title = 'GFR Push Test';
  let body = 'Sandbox notification received. Not a real incident.';
  try {
    if (event.data) {
      const data = event.data.json();
      if (typeof data.title === 'string' && data.title) title = data.title;
      if (typeof data.body === 'string' && data.body) body = data.body;
    }
  } catch (err) {
    // Malformed or non-JSON payload: fall back to the fixed defaults above.
    // Never rethrow from a push handler -- WebKit penalizes a push that
    // does not result in a visible notification.
  }
  event.waitUntil(
    self.registration.showNotification(title, {
      body,
      tag: 'gfr-pushtest',
    }),
  );
});

self.addEventListener('notificationclick', (event) => {
  event.notification.close();
  event.waitUntil(
    self.clients.matchAll({ type: 'window', includeUncontrolled: true }).then((clientList) => {
      for (const client of clientList) {
        if ('focus' in client) {
          return client.focus();
        }
      }
      if (self.clients.openWindow) {
        return self.clients.openWindow('/');
      }
    }),
  );
});
