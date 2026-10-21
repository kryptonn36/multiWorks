import test from 'node:test';
import assert from 'node:assert/strict';
import { connectProjectSocket } from './projectSocket.js';
const tick = () => new Promise((resolve) => setImmediate(resolve));

test('reconnect fetches a fresh ticket, resumes at current cursor, restores scene, and cleans timers', async () => {
  const sockets = [], timeouts = new Map(), intervals = new Map(), statuses = [], messages = [];
  let counter = 0, seq = 4;
  class Socket {
    static OPEN = 1;
    constructor(url) { this.url = new URL(url); this.readyState = 1; this.sent = []; sockets.push(this); }
    send(raw) { this.sent.push(JSON.parse(raw)); }
    close() { this.readyState = 3; this.onclose?.(); }
  }
  const timers = {
    setTimeout(fn) { timeouts.set(++counter, fn); return counter; }, clearTimeout(id) { timeouts.delete(id); },
    setInterval(fn) { intervals.set(++counter, fn); return counter; }, clearInterval(id) { intervals.delete(id); },
  };
  let tickets = 0;
  const client = connectProjectSocket({ projectId: 'project', getTicket: async () => ({ ticket: `ticket-${++tickets}` }), lastSeenSeq: () => seq, onMessage: (m) => messages.push(m), onStatus: (s) => statuses.push(s), WebSocketClass: Socket, timers, location: { href: 'https://app.example/', protocol: 'https:' } });
  await tick(); const first = sockets[0]; first.onopen(); client.viewScene('scene');
  assert.equal(first.url.protocol, 'wss:'); assert.equal(first.url.searchParams.get('last_seen_seq'), '4');
  assert.equal(first.url.searchParams.get('ticket'), 'ticket-1'); assert.equal(first.url.searchParams.has('token'), false);
  [...intervals.values()][0](); assert.equal(first.sent.at(-1).type, 'heartbeat');
  first.onmessage({ data: '{"type":"presence","users":[]}' }); await tick(); assert.equal(messages.length, 1);
  first.onmessage({ data: '{"type":"replay_complete","boundary_seq":4}' }); await tick();
  first.close(); assert.equal(intervals.size, 0); seq = 8;
  const [timer, reconnect] = [...timeouts][0]; timeouts.delete(timer); reconnect(); await tick();
  const second = sockets[1]; second.onopen();
  assert.equal(second.url.searchParams.get('ticket'), 'ticket-2'); assert.equal(second.url.searchParams.get('last_seen_seq'), '8');
  assert.deepEqual(second.sent[0], { type: 'view_scene', scene_id: 'scene' });
  client.close(); assert.equal(intervals.size, 0); assert.equal(timeouts.size, 0); assert.ok(statuses.includes('online'));
});

test('closing while ticket fetch is pending never opens a socket', async () => {
  let resolveTicket, opened = false;
  const client = connectProjectSocket({ projectId: 'p', getTicket: () => new Promise((resolve) => { resolveTicket = resolve; }), lastSeenSeq: () => 0, onMessage() {}, onStatus() {}, WebSocketClass: class { constructor() { opened = true; } } });
  client.close(); resolveTicket({ ticket: 'unused' }); await tick(); assert.equal(opened, false);
});

test('handoff waits for successful ordered delivery and revocation bypasses slow replay', async () => {
  let socket, release;
  const statuses = [], received = [], scheduled = new Set();
  class Socket {
    static OPEN = 1;
    constructor() { socket = this; this.readyState = 1; }
    send() {}
    close() { this.readyState = 3; this.onclose?.({ code: 1008 }); }
  }
  const timers = { setInterval() { scheduled.add('heartbeat'); return 'heartbeat'; }, clearInterval(id) { scheduled.delete(id); }, setTimeout() { scheduled.add('retry'); return 'retry'; }, clearTimeout(id) { scheduled.delete(id); } };
  const client = connectProjectSocket({ projectId: 'p', getTicket: async () => ({ ticket: 'one-use' }), lastSeenSeq: () => 0, onStatus: (status) => statuses.push(status), onMessage: async (message) => {
    received.push(message.type);
    if (message.type === 'replay') await new Promise((resolve) => { release = resolve; });
  }, timers, WebSocketClass: Socket, location: { href: 'http://localhost/', protocol: 'http:' } });
  await tick(); socket.onopen();
  assert.equal(statuses.at(-1), 'syncing');
  socket.onmessage({ data: '{"type":"replay_start","boundary_seq":1}' });
  socket.onmessage({ data: '{"type":"replay","operations":[]}' });
  socket.onmessage({ data: '{"type":"replay_complete","boundary_seq":1}' });
  await tick(); assert.equal(statuses.includes('online'), false);
  socket.onmessage({ data: '{"type":"permissions","access_revoked":true}' });
  assert.equal(statuses.at(-1), 'revoked'); assert.equal(scheduled.size, 0);
  release(); await tick(); assert.equal(statuses.includes('online'), false);
  assert.deepEqual(received, ['replay_start', 'replay', 'permissions']); client.close();
});

test('a failed handoff application never reports online', async () => {
  let socket;
  const statuses = [];
  class Socket { static OPEN = 1; constructor() { socket = this; this.readyState = 1; } send() {} close() {} }
  const client = connectProjectSocket({ projectId: 'p', getTicket: async () => ({ ticket: 'ticket' }), lastSeenSeq: () => 0, onStatus: (s) => statuses.push(s), onMessage: async () => { throw new Error('Missing operations'); }, WebSocketClass: Socket, location: { href: 'http://localhost/', protocol: 'http:' } });
  await tick(); socket.onopen(); socket.onmessage({ data: '{"type":"replay_complete","boundary_seq":7}' }); await tick();
  assert.equal(statuses.includes('online'), false); assert.equal(statuses.at(-1), 'error'); client.close();
});
