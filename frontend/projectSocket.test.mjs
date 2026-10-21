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
  first.onmessage({ data: '{"type":"presence","users":[]}' }); assert.equal(messages.length, 1);
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
