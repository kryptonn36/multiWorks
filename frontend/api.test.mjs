import test from 'node:test';
import assert from 'node:assert/strict';
import { createClient } from './api.js';

const json = (data, status = 200) => new Response(JSON.stringify(data), { status, headers: { 'content-type': 'application/json' } });
function storage() {
  const values = new Map();
  return { getItem: (key) => values.get(key), setItem: (key, value) => values.set(key, value), removeItem: (key) => values.delete(key) };
}

test('registration signs in separately and only persists the token', async () => {
  const calls = [];
  const session = storage();
  const client = createClient({ storage: session, fetcher: async (path, options) => {
    calls.push({ path, ...options });
    return path.endsWith('/register') ? json({ user: { id: 'user' } }, 201) : json({ user: { id: 'user' }, access_token: 'token' });
  } });
  assert.deepEqual(await client.register('person@example.com', 'password123'), { id: 'user' });
  assert.deepEqual(calls.map((c) => [c.path, c.method, JSON.parse(c.body)]), [
    ['/backend/api/auth/register', 'POST', { email: 'person@example.com', password: 'password123' }],
    ['/backend/api/auth/login', 'POST', { email: 'person@example.com', password: 'password123' }],
  ]);
  assert.equal(session.getItem('multiworks.token'), 'token');
  client.signOut(); assert.equal(client.authenticated, false); assert.equal(session.getItem('multiworks.token'), undefined);
});

test('partial registration failure prompts sign in without repeating registration', async () => {
  const client = createClient({ fetcher: async (path) => path.endsWith('/register') ? json({ user: {} }, 201) : json({ error: 'temporarily unavailable' }, 500) });
  await assert.rejects(client.register('a@example.com', 'password123'), (error) => error.accountCreated && /account was created/.test(error.message));
  assert.equal(client.authenticated, false);
});

test('protected requests attach the token and invalidate expired sessions', async () => {
  const session = storage(); session.setItem('multiworks.token', 'expired');
  let unauthorized = 0;
  const client = createClient({ storage: session, onUnauthorized: () => unauthorized++, fetcher: async (path, options) => {
    assert.equal(path, '/backend/api/workspaces/workspace-id/members');
    assert.equal(options.headers.Authorization, 'Bearer expired');
    assert.equal(options.method, 'POST');
    assert.deepEqual(JSON.parse(options.body), { email: 'person@example.com', role: 'member' });
    return json({ error: 'invalid or expired access token' }, 401);
  } });
  await assert.rejects(client.request('add-workspace-member', { context: { workspaceID: 'workspace-id' }, body: { email: 'person@example.com', role: 'member' } }), /session has expired/);
  assert.equal(unauthorized, 1); assert.equal(client.authenticated, false);
});

test('invalid login, forbidden access, and server failures remain errors', async () => {
  for (const status of [400, 401, 403, 409, 500]) {
    const client = createClient({ fetcher: async () => json({ error: 'Request rejected' }, status) });
    await assert.rejects(client.request('login', { body: { email: 'a@example.com', password: 'bad' } }), (error) => error.status === status && error.message === 'Request rejected');
  }
});

test('network errors, non-JSON responses, and unavailable service get readable messages', async () => {
  for (const [fetcher, expected] of [
    [async () => { throw new TypeError('fetch failed'); }, /Unable to connect/],
    [async () => new Response('<html>Error</html>', { status: 500 }), /unexpected response/],
    [async () => json({ error: 'internal proxy details' }, 502), /service is unavailable/],
  ]) await assert.rejects(createClient({ fetcher }).request('health'), expected);
});

test('late unauthorized response cannot clear a newer login', async () => {
  const session = storage(); session.setItem('multiworks.token', 'old-token');
  let finishOld;
  const oldResponse = new Promise((resolve) => { finishOld = resolve; });
  const client = createClient({ storage: session, fetcher: async (path) => path.endsWith('/login') ? json({ access_token: 'new-token', user: { id: 'new-user' } }) : oldResponse });
  const pending = client.request('me');
  await client.signIn('new@example.com', 'password123');
  finishOld(json({ error: 'expired' }, 401));
  await assert.rejects(pending, /session has expired/);
  assert.equal(session.getItem('multiworks.token'), 'new-token');
});

test('membership inputs reach the backend unchanged, including invalid values', async () => {
  for (const [id, body] of [
    ['add-workspace-member', { email: ' not-an-email ', role: 'unsupported-role' }],
    ['add-workspace-member', { email: '', role: '' }],
    ['add-project-member', { email: 'not-an-email', role: 'owner' }],
  ]) {
    const client = createClient({ fetcher: async (_path, options) => {
      assert.deepEqual(JSON.parse(options.body), body);
      return json({ error: 'Backend validation rejected this input' }, 400);
    } });
    await assert.rejects(client.request(id, { context: { workspaceID: 'workspace', projectID: 'project' }, body }), (error) => error.status === 400 && error.message === 'Backend validation rejected this input');
  }
});
