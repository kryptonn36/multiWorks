import test from 'node:test';
import assert from 'node:assert/strict';
import { createStage8 } from './stage8.js';

const escape = (s) => String(s ?? '').replaceAll('<', '&lt;').replaceAll('>', '&gt;');
test('ready status survives late upload data, authenticated blob URLs are revoked on disposal', async () => {
  let requests = 0;
  const feature = createStage8({ escape, onChange() {}, api: { async request(id) {
    if (id === 'assets') return { assets: [], has_more: false };
    if (id === 'proposals') return { proposals: [], has_more: false };
    if (id === 'asset-thumbnail') { requests++; return new Blob(['image'], { type: 'image/jpeg' }); }
    throw new Error(id);
  } } });
  await feature.load('project');
  const item = { id: 'asset', project_id: 'project', status: 'ready', updated_at: '2026-10-22T00:00:02Z', mime_type: 'image/png', size_bytes: 10 };
  feature.message({ type: 'asset_status', asset: item });
  await new Promise(resolve => setImmediate(resolve));
  feature.message({ type: 'asset_status', asset: { ...item, status: 'processing', updated_at: '2026-10-22T00:00:01Z' } });
  const html = feature.shotAssets({ id: 'shot', asset_ids: ['asset'] });
  assert.match(html, /data-asset-status="ready"/);
  const url = html.match(/src="(blob:[^"]+)"/)[1];
  assert.equal(await (await fetch(url)).text(), 'image');
  assert.equal(requests, 1);
  feature.dispose();
  await assert.rejects(fetch(url));
});

test('foreign project messages and old pending proposal data cannot replace final decisions', async () => {
  const feature = createStage8({ escape, onChange() {}, api: { async request(id) { return id === 'assets' ? { assets: [] } : { proposals: [] }; } } });
  await feature.load('project');
  const proposal = { id: 'p', project_id: 'project', instruction: '<script>unsafe</script>', mode: 'normal', status: 'approved', operations: [], base_seq: 0, created_at: '2026-10-22', updated_at: '2026-10-22T00:00:02Z' };
  feature.message({ type: 'agent_proposal', proposal });
  feature.message({ type: 'agent_proposal', proposal: { ...proposal, status: 'pending', updated_at: '2026-10-22T00:00:01Z' } });
  feature.message({ type: 'agent_proposal', proposal: { ...proposal, id: 'foreign', project_id: 'other', instruction: 'Private other project' } });
  assert.match(feature.panel(), /data-proposal-status="approved"/);
  assert.match(feature.panel(), /&lt;script&gt;/);
  assert.doesNotMatch(feature.panel(), /Private other project|<script>/);
  feature.dispose();
});

test('asynchronous proposals show progress and stale warnings without applying draft operations', async () => {
  const feature = createStage8({ escape, onChange() {}, getSequence: () => 8, sync() { throw new Error('Drafts must not apply'); }, api: { async request(id) { return id === 'assets' ? { assets: [] } : { proposals: [] }; } } });
  await feature.load('project');
  const proposal = { id: 'async', project_id: 'project', instruction: 'Add a shot', mode: 'normal', status: 'generating', proposed_ops: [], base_seq: 7, created_at: '2026-10-22', updated_at: '2026-10-22T00:00:01Z' };
  feature.message({ type: 'agent_proposal', proposal });
  assert.match(feature.panel(), /Generating proposal/);
  assert.match(feature.panel(), /data-proposal-warning/);
  feature.message({ type: 'agent_proposal', proposal: { ...proposal, status: 'pending', updated_at: '2026-10-22T00:00:02Z', proposed_ops: [{ type: 'shot.create', payload: { prompt: '<Draft>', scene_id: 'scene', shot_id: 'shot' } }] } });
  assert.match(feature.panel(), /data-proposal-status="pending"/);
  assert.match(feature.panel(), /Create shot/);
  assert.match(feature.panel(), /&lt;Draft&gt;/);
  feature.message({ type: 'agent_proposal', proposal: { ...proposal, updated_at: '2026-10-22T00:00:03Z' } });
  assert.match(feature.panel(), /data-proposal-status="pending"/);
  feature.dispose();
});

test('only generating proposal status is polled; polling stops on leaving the project', async (t) => {
  t.mock.timers.enable({ apis: ['setTimeout'] });
  const calls = [];
  const proposal = { id: 'async', project_id: 'project', instruction: 'Add a shot', status: 'generating', proposed_ops: [], base_seq: 0, created_at: '2026-10-22', updated_at: '2026-10-22T00:00:01Z' };
  const feature = createStage8({ escape, onChange() {}, api: { async request(id) {
    calls.push(id);
    if (id === 'assets') return { assets: [] };
    if (id === 'proposals') return { proposals: [] };
    if (id === 'proposal') return { ...proposal, status: 'pending', updated_at: '2026-10-22T00:00:02Z' };
    throw new Error(id);
  } } });
  await feature.load('project');
  feature.message({ type: 'agent_proposal', proposal });
  t.mock.timers.tick(1500); await new Promise(resolve => setImmediate(resolve));
  assert.deepEqual(calls, ['assets', 'proposals', 'proposal']);
  assert.match(feature.panel(), /data-proposal-status="pending"/);
  t.mock.timers.tick(5000); await new Promise(resolve => setImmediate(resolve));
  assert.equal(calls.length, 3);
  feature.message({ type: 'agent_proposal', proposal: { ...proposal, id: 'second' } });
  feature.dispose(); t.mock.timers.tick(5000); await new Promise(resolve => setImmediate(resolve));
  assert.equal(calls.length, 3);
});
