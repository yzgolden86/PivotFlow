const test = require('node:test');
const assert = require('node:assert/strict');
const Auth = require('./session.js');

function storage(initial = {}) {
  const values = new Map(Object.entries(initial));
  return { getItem: (key) => values.has(key) ? values.get(key) : null, setItem: (key, value) => values.set(key, String(value)), removeItem: (key) => values.delete(key) };
}

test('all login redirects are contained in the new console', () => {
  const origin = 'https://pivotflow.example';
  assert.equal(Auth.getSafeConsolePath('/web/console/#/sites', origin), '/web/console/#/sites');
  assert.equal(Auth.getSafeConsolePath('/web/index.html', origin), '/web/console/');
  assert.equal(Auth.getSafeConsolePath('/web/logs.html?range=today', origin), '/web/console/');
  assert.equal(Auth.getSafeConsolePath('https://evil.example/steal', origin), '/web/console/');
  assert.equal(Auth.getSafeConsolePath('//evil.example/steal', origin), '/web/console/');
});

test('admin session storage clears legacy role state', () => {
  const target = storage({ pivotflow_api_token: 'legacy' });
  Auth.storeAdminSession(target, { token: 'session', expiresIn: 60 }, 1000);
  assert.equal(target.getItem(Auth.TOKEN_KEY), 'session');
  assert.equal(target.getItem(Auth.EXPIRY_KEY), '61000');
  assert.equal(target.getItem(Auth.ROLE_KEY), 'admin');
  assert.equal(target.getItem('pivotflow_api_token'), null);
  assert.equal(Auth.hasUsableSession(target, 2000), true);
  assert.equal(Auth.hasUsableSession(target, 62000), false);
});
