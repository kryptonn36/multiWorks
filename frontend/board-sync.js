// Only this module advances the replay cursor. current_seq is intentionally unused.
function ordered(items) {
  return items.sort((a, b) => a.position - b.position || Date.parse(a.created_at) - Date.parse(b.created_at) || a.id.localeCompare(b.id));
}
function reorder(items, ids, timestamp) {
  if (!Array.isArray(ids) || ids.length !== items.length || new Set(ids).size !== ids.length) throw new Error('Cannot apply an incomplete or duplicate order.');
  return ids.map((id, position) => {
    const item = items.find((item) => item.id === id);
    if (!item) throw new Error('Cannot apply order: an item is missing locally.');
    return { ...item, position, updated_at: timestamp };
  });
}
export function applyBoardOperation(board, operation) {
  const next = structuredClone(board);
  const p = operation.payload;
  if (!p || typeof p !== 'object' || Array.isArray(p)) throw new Error('Invalid operation payload in replay.');
  const scene = () => {
    const item = next.scenes.find((item) => item.id === p.scene_id);
    if (!item) throw new Error('Cannot apply operation: scene is missing locally.');
    return item;
  };
  const shot = (parent) => {
    const item = parent.shots.find((item) => item.id === p.shot_id);
    if (!item) throw new Error('Cannot apply operation: shot is missing locally.');
    return item;
  };
  const update = (item, keys) => {
    for (const key of keys) {
      // Backend pointers/COALESCE preserve omitted and null fields.
      if (p[key] !== undefined && p[key] !== null) {
        if (typeof p[key] !== 'string') throw new Error('Invalid field type in replay.');
        item[key] = p[key];
      }
    }
    item.updated_at = operation.created_at;
  };
  const metadata = { created_by: operation.actor_id, created_at: operation.created_at, updated_at: operation.created_at };
  switch (operation.type) {
    case 'restore_board': {
      const restored = p.board_state;
      if (!restored || restored.project_id !== operation.project_id || !Array.isArray(restored.scenes) || restored.scenes.some((scene) => !Array.isArray(scene.shots))) throw new Error('Invalid restored board in operation.');
      return { ...structuredClone(restored), last_seq: operation.seq };
    }
    case 'scene.create':
      if (!p.scene_id || typeof p.title !== 'string' || next.scenes.some((s) => s.id === p.scene_id)) throw new Error('Cannot apply scene creation.');
      next.scenes.push({ id: p.scene_id, project_id: operation.project_id, title: p.title.trim(), description: p.description ?? '', position: Math.max(-1, ...next.scenes.map((s) => s.position)) + 1, shots: [], ...metadata });
      ordered(next.scenes); break;
    case 'scene.update': update(scene(), ['title', 'description']); break;
    case 'scene.delete': scene(); next.scenes = next.scenes.filter((s) => s.id !== p.scene_id); break;
    case 'scene.reorder': next.scenes = reorder(next.scenes, p.scene_ids, operation.created_at); break;
    case 'shot.create': {
      const parent = scene();
      if (!p.shot_id || parent.shots.some((s) => s.id === p.shot_id)) throw new Error('Cannot apply shot creation.');
      parent.shots.push({ asset_ids: [], id: p.shot_id, scene_id: p.scene_id, prompt: p.prompt ?? '', style: p.style ?? '', notes: p.notes ?? '', image_url: p.image_url ?? null, position: Math.max(-1, ...parent.shots.map((s) => s.position)) + 1, ...metadata });
      ordered(parent.shots); break;
    }
    case 'asset.attach': {
      const target = shot(scene());
      if (typeof p.asset_id !== 'string' || !p.asset_id) throw new Error('Invalid asset attachment in replay.');
      target.asset_ids = [...new Set([...(target.asset_ids || []), p.asset_id])];
      break;
    }
    case 'shot.update': update(shot(scene()), ['prompt', 'style', 'notes', 'image_url']); break;
    case 'shot.delete': { const parent = scene(); shot(parent); parent.shots = parent.shots.filter((s) => s.id !== p.shot_id); break; }
    case 'shot.reorder': { const parent = scene(); parent.shots = reorder(parent.shots, p.shot_ids, operation.created_at); break; }
    default: throw new Error(`Unsupported replay operation: ${operation.type}`);
  }
  return next;
}
function sequence(value) {
  if (!Number.isSafeInteger(value) || value < 0) throw new Error('Invalid or unsafe sequence in server response.');
  return value;
}
export function tabClientID(storage) {
  let id;
  try { id = storage?.getItem('multiworks.ops.client'); } catch { /* Optional storage. */ }
  if (!id) {
    id = crypto.randomUUID();
    try { storage?.setItem('multiworks.ops.client', id); } catch { /* In-memory session. */ }
  }
  return id;
}
export function createBoardSync({ api, projectID, clientID, storage, userID = '', pageSize = 100, onChange = () => {} }) {
  const storageKey = `multiworks.ops.pending.${userID}.${projectID}`;
  let board = null;
  let lastSeq = 0;
  let stopped = false;
  let syncing;
  let writing = false;
  let pending = null;
  const buffered = new Map();
  try { pending = JSON.parse(storage?.getItem(storageKey) || 'null'); } catch { /* Invalid/unavailable storage. */ }
  function persist() {
    try { pending ? storage?.setItem(storageKey, JSON.stringify(pending)) : storage?.removeItem(storageKey); } catch { /* Retry remains available in memory. */ }
  }
  function active() { if (stopped) throw new Error('Storyboard view changed.'); }
  async function initialize() {
    active();
    const snapshot = await api.request('board', { context: { projectID } });
    active();
    const checkpoint = sequence(snapshot.last_seq);
    if (snapshot.project_id !== projectID || !Array.isArray(snapshot.scenes) || snapshot.scenes.some((s) => !Array.isArray(s.shots))) throw new Error('Invalid board snapshot.');
    // This checkpoint describes a complete, atomically read board, not a replay page.
    board = structuredClone(snapshot); lastSeq = checkpoint;
    await sync();
  }
  async function replay() {
    active();
    if (!board) throw new Error('Load a board snapshot before replay.');
    for (;;) {
      const beforePage = lastSeq;
      const page = await api.request('replay-ops', { context: { projectID }, query: { after_seq: lastSeq, limit: pageSize } });
      active();
      if (!Array.isArray(page.operations) || typeof page.has_more !== 'boolean') throw new Error('Invalid replay response.');
      ingest(page.operations);
      if (!page.has_more) {
        if (buffered.size) throw new Error(`Replay gap: expected sequence ${lastSeq + 1}.`);
        return;
      }
      if (lastSeq === beforePage) throw new Error('Replay made no progress despite has_more.');
    }
  }
  function ingest(operations) {
    active();
    if (!board) throw new Error('Load a board snapshot before replay.');
    if (!Array.isArray(operations)) throw new Error('Invalid operations message.');
    for (const operation of [...operations].sort((a, b) => sequence(a.seq) - sequence(b.seq))) {
      if (operation.project_id !== projectID) throw new Error('Replay returned an operation for a different project.');
      const seq = sequence(operation.seq);
      if (seq > lastSeq) buffered.set(seq, operation);
    }
    let changed = false;
    try {
      while (buffered.has(lastSeq + 1)) {
        const seq = lastSeq + 1;
        const next = applyBoardOperation(board, buffered.get(seq));
        next.last_seq = seq;
        board = next; lastSeq = seq; buffered.delete(seq); changed = true;
      }
    } finally { if (changed) onChange(board); }
  }
  async function receive(operations, { hasMore = false } = {}) {
    ingest(operations);
    if (buffered.size || hasMore) await sync();
  }
  function sync() {
    if (syncing) return syncing;
    syncing = replay().finally(() => { syncing = null; });
    return syncing;
  }
  async function finishPending() {
    active();
    if (!pending) return;
    if (pending.acceptedSeq === undefined) {
      const request = pending.request;
      const response = request.type === 'restore_board'
        ? await api.request('restore-history', { context: { projectID, seq: request.payload.seq }, body: { client_id: request.client_id, op_id: request.op_id, base_seq: request.base_seq } })
        : await api.request('submit-op', { context: { projectID }, body: request });
      active();
      if (response.operation?.project_id !== projectID || response.operation.client_id !== pending.request.client_id || response.operation.op_id !== pending.request.op_id) throw new Error('Unexpected operation acknowledgement.');
      pending.acceptedSeq = sequence(response.operation.seq); persist();
    }
    await sync();
    active();
    if (lastSeq < pending.acceptedSeq) throw new Error('Write was saved but has not been replayed yet. Retry synchronization.');
    pending = null; persist();
  }
  async function submit(type, payload, opID = crypto.randomUUID(), { baseSeq = lastSeq } = {}) {
    active();
    if (!board) throw new Error('Load the storyboard before submitting changes.');
    if (writing) throw new Error('A storyboard write is already in progress.');
    if (pending && (pending.request.op_id !== opID || pending.request.type !== type || JSON.stringify(pending.request.payload) !== JSON.stringify(payload))) throw new Error('A previous write needs confirmation. Close this form and use Retry pending write before making another change.');
    if (!pending) {
      pending = { request: { client_id: clientID, op_id: opID, base_seq: baseSeq, type, payload: structuredClone(payload) } }; persist();
    }
    writing = true;
    try { await finishPending(); }
    catch (error) {
      // Definitive rejections did not mutate the board. Network/5xx outcomes may have committed.
      if (pending?.acceptedSeq === undefined && error.status >= 400 && error.status < 500) { pending = null; persist(); }
      throw error;
    } finally { writing = false; }
  }
  async function retry() {
    active();
    if (writing) throw new Error('A storyboard write is already in progress.');
    writing = true;
    try { await finishPending(); }
    catch (error) {
      if (pending?.acceptedSeq === undefined && error.status >= 400 && error.status < 500) { pending = null; persist(); }
      throw error;
    } finally { writing = false; }
  }
  return { initialize, sync, receive, submit, retry, stop: () => { stopped = true; }, get board() { return board; }, get lastSeq() { return lastSeq; }, get pending() { return pending; } };
}
