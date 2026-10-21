import test from 'node:test';
import assert from 'node:assert/strict';
import { applyBoardOperation, createBoardSync } from './board-sync.js';
const snapshot = (last_seq = 0, scenes = []) => ({ project_id: 'project', last_seq, scenes });
const op = (seq, type = 'scene.create', payload = { scene_id: `scene-${seq}`, title: `Scene ${seq}` }) => ({ project_id: 'project', seq, type, payload, actor_id: 'actor', created_at: '2026-10-21T00:00:00Z' });
const page = (operations = [], has_more = false, current_seq = 1000) => ({ operations, has_more, current_seq });
function engine(request, extra = {}) { return createBoardSync({ api: { request }, projectID: 'project', clientID: 'client', ...extra }); }
function storage() { const values = new Map(); return { getItem: (key) => values.get(key), setItem: (key, value) => values.set(key, value), removeItem: (key) => values.delete(key) }; }

test('replay sorts ascending, pages from actual last operation, and ignores current_seq', async () => {
  const cursors = [];
  const sync = engine(async (id, options) => {
    if (id === 'board') return snapshot();
    cursors.push(options.query.after_seq);
    return cursors.length === 1 ? page([op(2), op(1)], true) : page([op(2), op(3)]);
  });
  await sync.initialize();
  assert.deepEqual(cursors, [0, 2]); assert.equal(sync.lastSeq, 3);
  assert.deepEqual(sync.board.scenes.map((s) => s.id), ['scene-1', 'scene-2', 'scene-3']);
  await sync.sync(); assert.equal(cursors.at(-1), 3); assert.equal(sync.lastSeq, 3);
});

test('an empty replay response does not advance to current_seq', async () => {
  const sync = engine(async (id) => id === 'board' ? snapshot(7) : page());
  await sync.initialize(); assert.equal(sync.lastSeq, 7);
});

test('failed operation application preserves the last successful board and cursor, then resumes', async () => {
  let broken = true;
  const cursors = [];
  const sync = engine(async (id, options) => {
    if (id === 'board') return snapshot();
    cursors.push(options.query.after_seq);
    return broken ? page([op(1), op(2, 'unknown.type')]) : page([op(2, 'scene.update', { scene_id: 'scene-1', title: 'Changed' })]);
  });
  await assert.rejects(sync.initialize(), /Unsupported replay/);
  assert.equal(sync.lastSeq, 1); assert.equal(sync.board.scenes[0].title, 'Scene 1');
  broken = false; await sync.sync();
  assert.deepEqual(cursors, [0, 1]); assert.equal(sync.lastSeq, 2); assert.equal(sync.board.scenes[0].title, 'Changed');
});

test('gaps, foreign project operations, and pagination without progress stop without skipping', async () => {
  for (const [response, message] of [
    [page([op(1), op(3)]), /Replay gap/],
    [page([{ ...op(1), project_id: 'another-project' }]), /different project/],
    [page([], true), /no progress/],
  ]) {
    const sync = engine(async (id) => id === 'board' ? snapshot() : response);
    await assert.rejects(sync.initialize(), message);
    assert.equal(sync.lastSeq, response.operations.length === 2 ? 1 : 0);
  }
});

test('a failed reorder cannot partially mutate the existing local board', () => {
  let board = applyBoardOperation(snapshot(), op(1));
  board = applyBoardOperation(board, op(2));
  const original = structuredClone(board);
  assert.throws(() => applyBoardOperation(board, op(3, 'scene.reorder', { scene_ids: ['scene-2', 'missing'] })), /missing/);
  assert.deepEqual(board, original);
});

test('all operation reducers preserve partial fields and implement shot/scene order and cascading delete', () => {
  let board = applyBoardOperation(snapshot(), op(1));
  board = applyBoardOperation(board, op(2));
  board = applyBoardOperation(board, op(3, 'shot.create', { scene_id: 'scene-1', shot_id: 'shot-1', prompt: 'Rain', style: 'Blue', notes: 'Slow', image_url: null }));
  board = applyBoardOperation(board, op(4, 'shot.create', { scene_id: 'scene-1', shot_id: 'shot-2' }));
  board = applyBoardOperation(board, op(5, 'shot.update', { scene_id: 'scene-1', shot_id: 'shot-1', prompt: 'Close-up', image_url: null }));
  assert.equal(board.scenes[0].shots[0].style, 'Blue'); assert.equal(board.scenes[0].shots[0].notes, 'Slow');
  board = applyBoardOperation(board, op(6, 'shot.reorder', { scene_id: 'scene-1', shot_ids: ['shot-2', 'shot-1'] }));
  assert.deepEqual(board.scenes[0].shots.map((s) => s.id), ['shot-2', 'shot-1']);
  board = applyBoardOperation(board, op(7, 'scene.reorder', { scene_ids: ['scene-2', 'scene-1'] }));
  assert.deepEqual(board.scenes.map((s) => s.id), ['scene-2', 'scene-1']);
  board = applyBoardOperation(board, op(8, 'shot.delete', { scene_id: 'scene-1', shot_id: 'shot-2' }));
  assert.equal(board.scenes[1].shots.length, 1);
  board = applyBoardOperation(board, op(9, 'scene.delete', { scene_id: 'scene-1' }));
  assert.deepEqual(board.scenes.map((s) => s.id), ['scene-2']);
});

test('unknown POST outcomes retry the exact envelope without duplicate local creation', async () => {
  const sent = []; let committed;
  const sync = engine(async (id, options) => {
    if (id === 'board') return snapshot();
    if (id === 'replay-ops') return page(committed && options.query.after_seq === 0 ? [committed] : []);
    sent.push(structuredClone(options.body));
    committed = { ...op(1), client_id: 'client', op_id: 'intent' };
    if (sent.length === 1) throw new Error('connection lost after commit');
    return { operation: committed, duplicate: true, current_seq: 900 };
  });
  await sync.initialize();
  await assert.rejects(sync.submit('scene.create', committed?.payload || { scene_id: 'scene-1', title: 'Scene 1' }, 'intent'), /connection lost/);
  assert.equal(sync.lastSeq, 0); assert.ok(sync.pending);
  await sync.retry();
  assert.deepEqual(sent[0], sent[1]); assert.equal(sent[0].base_seq, 0);
  assert.equal(sync.lastSeq, 1); assert.equal(sync.board.scenes.length, 1); assert.equal(sync.pending, null);
});

test('submit acknowledgements never skip missing operations and an accepted write is not posted again', async () => {
  let postCount = 0; let visible = false;
  const sync = engine(async (id) => {
    if (id === 'board') return snapshot();
    if (id === 'replay-ops') return page(visible ? [op(1), op(2)] : []);
    postCount++;
    return { operation: { ...op(2), client_id: 'client', op_id: 'intent' }, current_seq: 500 };
  });
  await sync.initialize();
  await assert.rejects(sync.submit('scene.create', { scene_id: 'scene-2', title: 'Scene 2' }, 'intent'), /not been replayed/);
  assert.equal(sync.lastSeq, 0); assert.equal(postCount, 1);
  visible = true; await sync.retry();
  assert.equal(sync.lastSeq, 2); assert.equal(postCount, 1);
});

test('pending writes survive reconstruction with the same keys; snapshots bootstrap at a consistent checkpoint', async () => {
  const saved = storage(); const sent = []; let fail = true;
  const request = async (id, options) => {
    if (id === 'board') return snapshot(fail ? 0 : 1, fail ? [] : applyBoardOperation(snapshot(), op(1)).scenes);
    if (id === 'replay-ops') return page();
    sent.push(structuredClone(options.body));
    if (fail) throw new Error('network lost');
    return { operation: { ...op(1), client_id: 'client', op_id: 'intent' }, duplicate: true, current_seq: 900 };
  };
  const first = engine(request, { storage: saved }); await first.initialize();
  await assert.rejects(first.submit('scene.create', op(1).payload, 'intent'), /network lost/);
  first.stop(); fail = false;
  const restored = engine(request, { storage: saved }); await restored.initialize(); await restored.retry();
  assert.deepEqual(sent[0], sent[1]); assert.equal(restored.lastSeq, 1); assert.equal(restored.board.scenes.length, 1); assert.equal(restored.pending, null);
});

test('definitive backend rejection clears only the pending request and does not advance the cursor', async () => {
  const sync = engine(async (id) => {
    if (id === 'board') return snapshot();
    if (id === 'replay-ops') return page();
    throw Object.assign(new Error('editor or owner role required'), { status: 403 });
  });
  await sync.initialize(); await assert.rejects(sync.submit('scene.create', { title: '' }, 'intent'), /editor or owner/);
  assert.equal(sync.lastSeq, 0); assert.equal(sync.pending, null); assert.equal(sync.board.scenes.length, 0);
});
