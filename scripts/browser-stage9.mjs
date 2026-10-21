import assert from 'node:assert/strict';
import { execFile } from 'node:child_process';
import { promisify } from 'node:util';
import { randomBytes, randomUUID } from 'node:crypto';
import { mkdir, writeFile } from 'node:fs/promises';
import { fileURLToPath } from 'node:url';
import { deflateSync } from 'node:zlib';
import { launchBrowser } from './browser-driver.mjs';

const root = fileURLToPath(new URL('../', import.meta.url));
const base = `http://127.0.0.1:${process.env.TEST_FRONTEND_PORT || 15173}`;
const run = promisify(execFile);
const compose = (...args) => run('docker', ['compose', '--project-name', 'multiworks-stage9', '--file', `${root}docker-compose.test.yml`, '--profile', 'browser', ...args], { cwd: root, timeout: 60000 });
const results = [];
const pass = text => { results.push(text); console.log(`PASS ${text}`); };
async function api(path, token, body, expected = body ? 201 : 200) {
  const response = await fetch(`${base}/backend${path}`, { method: body ? 'POST' : 'GET', headers: { 'content-type': 'application/json', ...(token ? { authorization: `Bearer ${token}` } : {}) }, ...(body ? { body: JSON.stringify(body) } : {}), signal: AbortSignal.timeout(10000) });
  const data = await response.json(); assert.equal(response.status, expected, `${path}: ${response.status} ${data.error || ''}`); return data;
}
// Generate a tiny valid image fixture without an image-generation dependency.
function png() {
  function crc(data) { let crc = 0xffffffff; for (const byte of data) { crc ^= byte; for (let n = 0; n < 8; n++) crc = (crc >>> 1) ^ ((crc & 1) ? 0xedb88320 : 0); } return (crc ^ 0xffffffff) >>> 0; }
  function chunk(kind, data) { const content = Buffer.concat([Buffer.from(kind), data]); const length = Buffer.alloc(4); length.writeUInt32BE(data.length); const checksum = Buffer.alloc(4); checksum.writeUInt32BE(crc(content)); return Buffer.concat([length, content, checksum]); }
  const header = Buffer.alloc(13); header.writeUInt32BE(16); header.writeUInt32BE(16, 4); header[8] = 8; header[9] = 2;
  const pixels = Buffer.alloc(16 * (1 + 16 * 3), 120); for (let y = 0; y < 16; y++) pixels[y * 49] = 0;
  return Buffer.concat([Buffer.from([137, 80, 78, 71, 13, 10, 26, 10]), chunk('IHDR', header), chunk('IDAT', deflateSync(pixels)), chunk('IEND', Buffer.alloc(0))]);
}
const browser = await launchBrowser();
try {
  const configured = await (await fetch(`${base}/config`)).json();
  assert.equal(configured.target, 'http://test-api:8080', 'Browser checks require the isolated Stage 9 frontend.');
  const password = randomBytes(16).toString('hex');
  const people = [];
  for (const role of ['owner', 'editor']) {
    const email = `stage9-${role}-${randomUUID()}@example.invalid`;
    await api('/api/auth/register', '', { email, password });
    const login = await api('/api/auth/login', '', { email, password }, 200); people.push({ email, token: login.access_token });
  }
  const [owner, editor] = people;
  const workspace = await api('/api/workspaces', owner.token, { name: 'Stage 9 browser verification' });
  const project = await api(`/api/workspaces/${workspace.id}/projects`, owner.token, { name: 'Crash recovery and collaboration' });
  const projectPath = `/api/projects/${project.id}`;
  await api(`/api/workspaces/${workspace.id}/members`, owner.token, { email: editor.email, role: 'member' }, 200);
  await api(`${projectPath}/members`, owner.token, { email: editor.email, role: 'editor' }, 200);
  const a = await browser.newPage(base), b = await browser.newPage(base);
  for (const [index, page] of [a, b].entries()) {
    await page.navigate('#/login'); await page.wait('!!document.querySelector("#auth-form")');
    await page.fill('#auth-form [name=email]', people[index].email); await page.fill('#auth-form [name=password]', password);
    await page.evaluate('document.querySelector("#auth-form").requestSubmit()'); await page.wait('!document.querySelector("#auth-form")');
    await page.navigate(`#/project/${project.id}`); await page.wait('document.querySelector(".presence-bar .badge")?.textContent==="online"');
  }
  pass('Two isolated browser sessions sign in through the real login form');
  const applied = async sequence => { for (const page of [a, b]) await page.wait(`document.querySelector('[data-applied-seq="${sequence}"]')`); };
  const closeModal = page => page.click('#modal [data-action="close-modal"]');
  const submit = async page => { await page.submit(); await page.wait('!document.querySelector("#modal").open'); };
  await a.click('[data-board-action="create-scene"]'); await a.fill('#modal-form [name=title]', 'Opening'); await submit(a); await applied(1);
  const scene = await a.evaluate('document.querySelector(".scene-card").dataset.sceneId');
  await Promise.all([
    api(`${projectPath}/ops`, owner.token, { client_id: 'browser-a', op_id: randomUUID(), base_seq: 1, type: 'scene.update', payload: { scene_id: scene, title: 'Concurrent title' } }),
    api(`${projectPath}/ops`, editor.token, { client_id: 'browser-b', op_id: randomUUID(), base_seq: 1, type: 'scene.update', payload: { scene_id: scene, description: 'Concurrent description' } }),
  ]); await applied(3);
  assert.equal(await b.evaluate('document.querySelector(".scene-heading h3").textContent'), 'Concurrent title');
  assert.equal(await b.evaluate('document.querySelector(".scene-description").textContent'), 'Concurrent description');
  await a.click('[data-board-action="view-scene"]');
  await b.wait('[...document.querySelectorAll(".presence-person")].some(person=>person.textContent.includes("Concurrent title"))');
  pass('Live collaboration, viewed-scene presence, and concurrent field edits converge in both sessions');
  for (const prompt of ['First shot', 'Second shot']) { await a.click('[data-board-action="create-shot"]'); await a.fill('#modal-form [name=prompt]', prompt); await submit(a); }
  await applied(5);
  const shots = await a.evaluate('[...document.querySelectorAll(".shot-card")].map(card=>card.dataset.shotId)');
  const image = png();
  for (const shot of shots) {
    await a.click(`[data-board-action="upload-asset"][data-shot="${shot}"]`);
    await a.evaluate(`{const file=new File([new Uint8Array(${JSON.stringify([...image])})],'fixture.png',{type:'image/png'});const files=new DataTransfer();files.items.add(file);document.querySelector('#modal-form [name=file]').files=files.files;}`); await submit(a);
  }
  await applied(7); for (const page of [a, b]) await page.wait('document.querySelectorAll("[data-asset-status=ready]").length===2&&[...document.querySelectorAll(".asset-thumbnail")].every(image=>image.naturalWidth>0)');
  const assets = await api(`${projectPath}/assets`, owner.token); assert.equal(assets.assets.length, 1);
  pass('Duplicate uploads share one asset and protected thumbnails update live');
  const editNotes = async (page, shot, text) => { await page.click(`[data-board-action="edit-shot"][data-shot="${shot}"]`); await page.fill('#modal-form [name=notes]', text); };
  await compose('stop', '-t', '2', 'test-api');
  await editNotes(a, shots[0], 'Recovered after graceful restart'); await a.submit(); await a.wait('document.querySelector("#modal-error")?.textContent.length>0'); await closeModal(a);
  assert.ok(await a.evaluate('!!document.querySelector("[data-board-action=retry-write]")'));
  await compose('up', '-d', '--wait', 'test-api');
  for (const page of [a, b]) await page.wait('document.querySelector(".presence-bar .badge")?.textContent==="online"');
  await a.click('[data-board-action="retry-write"]'); await applied(8);
  pass('Graceful backend restart preserves state and an unconfirmed edit retries successfully');
  // Drop a response after the server committed it. Retry after SIGKILL must use
  // the original idempotency key and recover exactly one logical operation.
  await a.call('Fetch.enable', { patterns: [{ urlPattern: '*/backend/api/projects/*/ops', requestStage: 'Response' }] });
  const intercept = async ({ data }) => {
    const event = JSON.parse(data); if (event.method !== 'Fetch.requestPaused') return;
    a.socket.removeEventListener('message', intercept);
    await a.call('Fetch.failRequest', { requestId: event.params.requestId, errorReason: 'ConnectionClosed' }); await a.call('Fetch.disable');
  };
  a.socket.addEventListener('message', intercept);
  await editNotes(a, shots[1], 'Committed before abrupt restart'); await a.submit(); await a.wait('document.querySelector("#modal-error")?.textContent.length>0'); await closeModal(a);
  const pending = await a.evaluate('JSON.parse(Object.entries(sessionStorage).find(([key])=>key.startsWith("multiworks.ops.pending."))[1]).request');
  assert.equal((await api(`${projectPath}/board`, owner.token)).last_seq, 9);
  await compose('kill', '-s', 'SIGKILL', 'test-api'); await compose('up', '-d', '--wait', 'test-api');
  for (const page of [a, b]) await page.wait('document.querySelector(".presence-bar .badge")?.textContent==="online"');
  await a.click('[data-board-action="retry-write"]'); await a.wait('!document.querySelector("[data-board-action=retry-write]")'); await applied(9);
  let log = await api(`${projectPath}/ops?after_seq=0&limit=500`, owner.token);
  assert.equal(log.operations.filter(op => op.client_id === pending.client_id && op.op_id === pending.op_id).length, 1);
  assert.equal(log.operations.length, 9);
  for (const page of [a, b]) { assert.equal(await page.evaluate('document.querySelectorAll(".shot-card").length'), 2); await page.wait('[...document.querySelectorAll(".asset-thumbnail")].every(image=>image.naturalWidth>0)'); }
  pass('Abrupt backend termination recovers committed operations without duplicating a lost-acknowledgment write');
  async function generate(instruction) {
    await a.click('[data-board-action="create-proposal"]'); await a.fill('#modal-form [name=instruction]', instruction); await submit(a);
    await a.wait(`[...document.querySelectorAll('.proposal-card')].some(card=>card.innerText.includes(${JSON.stringify(instruction)})&&card.querySelector('[data-proposal-status]')?.dataset.proposalStatus!=='generating')`);
    return a.evaluate(`[...document.querySelectorAll('.proposal-card')].find(card=>card.innerText.includes(${JSON.stringify(instruction)})).dataset.proposalId`);
  }
  async function decide(id, action, succeeds = true) { await a.click(`[data-board-action="${action}-proposal"][data-proposal="${id}"]`); await a.submit(); await a.wait(succeeds ? '!document.querySelector("#modal").open' : 'document.querySelector("#modal-error")?.textContent.length>0'); }
  const proposal = await generate('Add a close-up shot for approval'); assert.equal((await api(`${projectPath}/board`, owner.token)).last_seq, 9);
  await decide(proposal, 'approve'); await applied(10);
  const stale = await generate('Add a stale proposal shot');
  await api(`${projectPath}/ops`, owner.token, { client_id: 'browser-human', op_id: randomUUID(), base_seq: 10, type: 'scene.update', payload: { scene_id: scene, description: 'Changed since proposal' } }); await applied(11);
  await a.wait(`document.querySelector('[data-proposal-id="${stale}"] [data-proposal-warning]')`); await decide(stale, 'approve', false); await closeModal(a);
  await a.wait(`document.querySelector('[data-proposal-id="${stale}"] [data-proposal-status=stale]')`);
  const rejected = await generate('Add a rejected proposal shot'); await decide(rejected, 'reject'); assert.equal((await api(`${projectPath}/board`, owner.token)).last_seq, 11);
  for (const token of ['invalid-json', 'missing-shot', 'oversized', 'timeout']) { const id = await generate(`Add a failure shot [mock:${token}]`); await a.wait(`document.querySelector('[data-proposal-id="${id}"] [data-proposal-status=failed]')`); }
  pass('Agent drafts stay unapplied until approval; rejection, stale checks, and failure tokens leave the board intact');
  // Exercise seq-N restore while another collaborator edits after preview.
  for (let seq=12;seq<=42;seq++) await api(`${projectPath}/ops`,owner.token,{client_id:'history-browser',op_id:randomUUID(),base_seq:0,type:'scene.update',payload:{scene_id:scene,title:`Frame ${seq}`}});
  await applied(42);
  await a.click('[data-board-action="toggle-history"]');
  const preview=async()=>{await a.wait('!!document.querySelector("#history-form")&&!document.querySelector("#history-form button").disabled');await a.fill('#history-seq','40');await a.evaluate('document.querySelector("#history-form").requestSubmit()');await a.wait('document.querySelector("[data-history-seq]")?.dataset.historySeq==="40"');};
  await preview();
  await api(`${projectPath}/ops`,editor.token,{client_id:'concurrent-history',op_id:randomUUID(),base_seq:42,type:'scene.update',payload:{scene_id:scene,title:'Concurrent edit at 43'}});await applied(43);
  await a.click('[data-board-action="restore-history"]');await a.submit();await a.wait('document.querySelector("#modal-error")?.textContent.length>0');await closeModal(a);
  await preview();await a.click('[data-board-action="restore-history"]');await submit(a);await applied(44);
  assert.equal(await b.evaluate('document.querySelector(".scene-heading h3").textContent'),'Frame 40');
  pass('Restore to seq 40 rejects a stale preview, then commits a new operation that converges in both sessions');
  await api(`${projectPath}/members`, owner.token, { email: editor.email, role: 'viewer' }, 200);
  await b.wait('document.querySelector(".project-facts strong")?.textContent.toLowerCase()==="viewer"');
  await b.click('[data-board-action="create-scene"]'); await b.fill('#modal-form [name=title]', 'Must be denied'); await b.submit(); await b.wait('document.querySelector("#modal-error")?.textContent.includes("editor or owner")'); await closeModal(b);
  assert.equal((await api(`${projectPath}/board`, owner.token)).last_seq, 44);
  pass('A mid-session role downgrade blocks writes through the UI with the existing JWT');
  log = await api(`${projectPath}/ops?after_seq=0&limit=500`, owner.token);
  assert.deepEqual(log.operations.map(op => op.seq), Array.from({ length: 44 }, (_, n) => n + 1));
  const download = await fetch(`${base}/backend${projectPath}/assets/${assets.assets[0].id}`, { headers: { authorization: `Bearer ${editor.token}` } });
  assert.equal(download.status, 200); assert.deepEqual(Buffer.from(await download.arrayBuffer()), image);
  await a.call('Emulation.setDeviceMetricsOverride', { width: 390, height: 844, deviceScaleFactor: 1, mobile: true });
  assert.equal(await a.evaluate('document.documentElement.scrollWidth<=innerWidth'), true); assert.deepEqual(a.errors, []); assert.deepEqual(b.errors, []);
  pass('Final board, operation ordering, persisted bytes, and mobile rendering are consistent');
  await mkdir(`${root}test-results`, { recursive: true });
  await writeFile(`${root}test-results/stage9-browser.json`, JSON.stringify({ results, projectID: project.id, appliedSequence: 44 }, null, 2));
  const screenshot = await a.call('Page.captureScreenshot', { format: 'png' });
  await writeFile(`${root}test-results/stage9-browser.png`, Buffer.from(screenshot.data, 'base64'));
} finally { await browser.close(); }
