import test from 'node:test';
import assert from 'node:assert/strict';
import http from 'node:http';
import { readFile } from 'node:fs/promises';
import { createServer } from './server.mjs';
import { endpoints, requestPath } from './endpoints.js';

function listen(server) {
  return new Promise((resolve, reject) => {
    server.once('error', reject);
    server.listen(0, '127.0.0.1', () => resolve(`http://127.0.0.1:${server.address().port}`));
  });
}
function close(server) {
  server.closeAllConnections();
  return new Promise((resolve) => server.close(resolve));
}

test('frontend route definitions cover every registered Go route', async () => {
  const source = await readFile(new URL('../backend/cmd/server/main.go', import.meta.url), 'utf8');
  const prefixes = { router: '', publicAuth: '/api/auth', protectedAuth: '/api/auth', api: '/api' };
  const routes = [...source.matchAll(/(router|publicAuth|protectedAuth|api)\.(GET|POST|PATCH|PUT|DELETE)\(\s*"([^"]+)"/g)]
    .map(([, group, method, path]) => `${method} ${prefixes[group]}${path}`).sort();
  assert.equal(routes.length, 32);
  assert.deepEqual(endpoints.map((e) => `${e.method} ${e.path}`).sort(), routes);
});

test('path parameters are required and safely encoded', () => {
  const endpoint = endpoints.find((e) => e.id === 'project');
  assert.throws(() => requestPath(endpoint, {}), /project ID/);
  assert.equal(requestPath(endpoint, { projectID: 'abc/?#' }), '/api/projects/abc%2F%3F%23');
});

test('local server serves assets and proxies API requests, bodies, tokens, and errors', async (t) => {
  const requests = [];
  const backend = http.createServer(async (req, res) => {
    let body = '';
    for await (const chunk of req) body += chunk;
    requests.push({ path: req.url, method: req.method, authorization: req.headers.authorization, body });
    res.writeHead(req.url.includes('/auth/me') ? 401 : req.method === 'POST' ? 201 : 200, { 'content-type': 'application/json' });
    res.end(JSON.stringify({ received: req.url, error: req.url.includes('/auth/me') ? 'invalid or expired access token' : undefined }));
  });
  const backendURL = await listen(backend);
  const frontend = createServer(backendURL);
  t.after(() => close(frontend));
  t.after(() => close(backend));
  const base = await listen(frontend);
  for (const path of ['/', '/app.js', '/api.js', '/endpoints.js', '/storyboard.js', '/stage8.js', '/board-sync.js', '/projectSocket.js', '/styles.css']) {
    const response = await fetch(`${base}${path}`);
    assert.equal(response.status, 200);
    assert.ok((await response.text()).length > 0);
  }
  assert.deepEqual(await (await fetch(`${base}/config`)).json(), { target: backendURL });
  for (const endpoint of endpoints.filter((e) => e.transport !== 'websocket')) {
    const path = requestPath(endpoint, { workspaceID: 'workspace-test', projectID: 'project-test', sceneID: 'scene-test', shotID: 'shot-test', assetID: 'asset-test', proposalID: 'proposal-test', seq: '3' });
    const body = ['POST', 'PATCH', 'PUT'].includes(endpoint.method) ? JSON.stringify({ name: 'Test', email: 'user@example.com', role: 'viewer', ids: ['scene-test'] }) : undefined;
    const response = await fetch(`${base}/backend${path}`, {
      method: endpoint.method,
      headers: { 'content-type': 'application/json', authorization: 'Bearer test-token' }, body,
    });
    assert.equal(response.status, endpoint.id === 'me' ? 401 : endpoint.method === 'POST' ? 201 : 200);
    assert.equal((await response.json()).received, path);
    assert.deepEqual(requests.at(-1), { path, method: endpoint.method, authorization: 'Bearer test-token', body: body || '' });
  }
  assert.equal((await (await fetch(`${base}/backend/api/projects/project/ops?after_seq=7&limit=1`)).json()).received, '/api/projects/project/ops?after_seq=7&limit=1');
  assert.equal((await fetch(`${base}/.env`)).status, 404);
  assert.equal((await fetch(`${base}/server.mjs`)).status, 404);
  const unauthorized = await fetch(`${base}/backend/api/auth/me`);
  assert.equal(unauthorized.status, 401);
  assert.equal((await unauthorized.json()).error, 'invalid or expired access token');
  await close(backend);
  const unavailable = await fetch(`${base}/backend/health`);
  assert.equal(unavailable.status, 502);
  assert.match((await unavailable.json()).error, /Cannot reach the API/);
});
