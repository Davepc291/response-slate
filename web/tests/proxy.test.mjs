import assert from 'node:assert/strict';
import { readFile } from 'node:fs/promises';
import test from 'node:test';

const proxyConfigUrl = new URL('../proxy.conf.json', import.meta.url);
const packageJsonUrl = new URL('../package.json', import.meta.url);
const angularJsonUrl = new URL('../angular.json', import.meta.url);

const loopbackHosts = new Set(['127.0.0.1', 'localhost', '::1', '[::1]']);

function hostOf(target) {
  return new URL(target).hostname;
}

test('proxy.conf.json only ever targets a loopback host', async () => {
  const proxy = JSON.parse(await readFile(proxyConfigUrl, 'utf8'));
  const contexts = Object.keys(proxy);

  assert.ok(contexts.length > 0, 'proxy.conf.json must define at least one proxied path');

  for (const context of contexts) {
    const entry = proxy[context];
    assert.ok(entry && typeof entry.target === 'string', `${context} must have a string target`);
    assert.ok(
      loopbackHosts.has(hostOf(entry.target)),
      `proxy target ${entry.target} for ${context} must be a loopback host, not a public/remote one`,
    );
  }
});

test('proxy.conf.json routes /api to the local Go API', async () => {
  const proxy = JSON.parse(await readFile(proxyConfigUrl, 'utf8'));

  assert.ok('/api' in proxy, 'proxy.conf.json must proxy the /api path used by auth-api.service.ts');
  assert.equal(proxy['/api'].target, 'http://127.0.0.1:8080');
});

test('a clearly named local-auth npm script serves Angular over HTTPS through the local proxy', async () => {
  const pkg = JSON.parse(await readFile(packageJsonUrl, 'utf8'));
  const scripts = pkg.scripts ?? {};

  const localAuthKey = Object.keys(scripts).find((name) => /local.?auth/i.test(name));
  assert.ok(localAuthKey, 'expected an npm script clearly named for local authentication testing');

  const command = scripts[localAuthKey];
  assert.match(command, /ng serve/);
  assert.match(command, /--ssl\b/);
  assert.match(command, /--proxy-config[= ]proxy\.conf\.json/);
});

test('adding the local-auth script does not change production build or deployment scripts', async () => {
  const pkg = JSON.parse(await readFile(packageJsonUrl, 'utf8'));
  const scripts = pkg.scripts ?? {};

  assert.equal(scripts.build, 'ng build');
  assert.equal(scripts.start, 'ng serve');
  assert.doesNotMatch(scripts.build, /proxy|ssl/i);
});

test('angular.json production build configuration is unchanged by local proxy support', async () => {
  const angular = JSON.parse(await readFile(angularJsonUrl, 'utf8'));
  const build = angular.projects.web.architect.build;

  assert.equal(build.defaultConfiguration, 'production');
  assert.deepEqual(Object.keys(build.configurations.production).sort(), ['budgets', 'outputHashing', 'serviceWorker']);
  assert.ok(
    !('proxyConfig' in (build.options ?? {})),
    'the build target must not reference a proxy configuration',
  );

  const serve = angular.projects.web.architect.serve;
  assert.ok(
    !('proxyConfig' in (serve.options ?? {})) && !('ssl' in (serve.options ?? {})),
    'serve options must not hardcode proxy/ssl: local-auth passes them as explicit CLI flags instead, so plain `npm start` is unaffected',
  );
});
