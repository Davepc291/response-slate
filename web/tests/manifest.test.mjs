import assert from 'node:assert/strict';
import { readFile } from 'node:fs/promises';
import test from 'node:test';

const manifestUrl = new URL('../public/manifest.webmanifest', import.meta.url);

test('installed PWA launches the dedicated mobile welcome experience', async () => {
  const manifest = JSON.parse(await readFile(manifestUrl, 'utf8'));

  assert.equal(manifest.start_url, '/mobile/welcome');
  assert.equal(manifest.id, '/mobile/');
  assert.equal(manifest.scope, '/');
});
