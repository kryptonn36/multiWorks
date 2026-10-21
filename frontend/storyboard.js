import { connectProjectSocket } from './projectSocket.js';
import { createBoardSync, tabClientID } from './board-sync.js';

// Storyboard UI shares the application's API client, modal, and feedback components.
export function createStoryboard({ api, escape: esc, showForm, closeModal, toast, storage, getUserID = () => '', getMembers = () => [], onRoleChange = () => {} }) {
  let projectID = '';
  let synchronizer;
  let socket;
  let livePaused = false;
  let status = 'offline';
  let presence = [];
  let viewedScene = '';
  let connectionError = '';
  let accessMessage = '';
  let handoffBoundary = null;
  let memberLookup = [];
  const clientID = tabClientID(storage);
  let scenes = [];
  let revision = 0;
  let busy = false;
  const root = () => document.getElementById('storyboard');
  const context = (sceneID, shotID) => ({ projectID, ...(sceneID ? { sceneID } : {}), ...(shotID ? { shotID } : {}) });
  const action = (name, label, sceneID = '', shotID = '', className = 'secondary') =>
    `<button type="button" class="${className}" data-board-action="${name}" data-scene="${esc(sceneID)}" data-shot="${esc(shotID)}">${label}</button>`;
  const orderButtons = (kind, sceneID, shotID = '') => `${action(`${kind}-up`, '↑', sceneID, shotID)}${action(`${kind}-down`, '↓', sceneID, shotID)}`;
  function imageURL(value) {
    try { const url = new URL(value); return ['https:', 'http:'].includes(url.protocol) ? url.href : ''; } catch { return ''; }
  }
  function shotCard(shot, index, scene) {
    const url = imageURL(shot.image_url);
    return `<article class="shot-card" data-shot-id="${esc(shot.id)}"><div class="shot-preview">${url ? `<img class="shot-image" src="${esc(url)}" alt="Shot ${index + 1}" loading="lazy" referrerpolicy="no-referrer">` : ''}<span class="shot-placeholder" ${url ? 'hidden' : ''}>◈<small>No image</small></span></div><div class="shot-content"><div class="section-heading"><h4>Shot ${index + 1}</h4><span class="small muted">Position ${esc(shot.position)}</span></div><p class="shot-prompt">${esc(shot.prompt || 'No prompt yet.')}</p><dl class="shot-details"><div><dt>Style</dt><dd>${esc(shot.style || '—')}</dd></div><div><dt>Notes</dt><dd>${esc(shot.notes || '—')}</dd></div></dl>${shot.image_url ? `<p class="image-url" title="${esc(shot.image_url)}">Image: ${esc(shot.image_url)}</p>` : ''}<div class="board-actions">${orderButtons('shot', scene.id, shot.id)}${action('edit-shot', 'Edit', scene.id, shot.id)}${action('delete-shot', 'Delete', scene.id, shot.id, 'danger-button')}</div></div></article>`;
  }
  function render() {
    if (!root()) return;
    root().innerHTML = `<div id="board-presence"></div><div class="section-heading"><div><h2>Storyboard <span class="count">${scenes.length} scenes</span></h2><p>Plan your scenes, arrange your shots, and build your story.</p></div><div class="heading-actions">${synchronizer?.pending ? action('retry-write', 'Retry pending write') : ''}${action('toggle-live', livePaused ? 'Reconnect live' : 'Pause live')}${action('reload', 'Sync changes')}${action('order-scenes', 'Edit scene order')}${action('create-scene', '+ Add scene', '', '', 'primary')}</div></div><p class="field-note"><span data-applied-seq="${synchronizer?.lastSeq ?? 0}">Applied sequence ${synchronizer?.lastSeq ?? 0}</span>. Owner/editor access is required for changes. All controls remain available so you can test backend permissions.</p>${scenes.length ? `<div class="scene-list">${scenes.map((scene, index) => `<section class="scene-card" data-scene-id="${esc(scene.id)}"><header class="scene-heading"><div><p class="eyebrow">SCENE ${index + 1} · POSITION ${esc(scene.position)}</p><h3>${esc(scene.title)}</h3><p class="scene-description">${esc(scene.description || 'No description yet.')}</p></div><div class="board-actions">${action('view-scene', viewedScene === scene.id ? 'Viewing' : 'View scene', scene.id)}${orderButtons('scene', scene.id)}${action('edit-scene', 'Edit scene', scene.id)}${action('delete-scene', 'Delete scene', scene.id, '', 'danger-button')}</div></header><div class="section-heading"><h4>${scene.shots.length} ${scene.shots.length === 1 ? 'shot' : 'shots'}</h4><div class="heading-actions">${action('order-shots', 'Edit shot order', scene.id)}${action('create-shot', '+ Add shot', scene.id, '', 'primary')}</div></div>${scene.shots.length ? `<div class="shot-grid">${scene.shots.map((shot, index) => shotCard(shot, index, scene)).join('')}</div>` : '<p class="board-empty">No shots in this scene. Add a shot to get started.</p>'}</section>`).join('')}</div>` : '<div class="empty"><span class="empty-symbol">◈</span><h3>Your story starts with a scene.</h3><p>Use Add scene to create the first scene in this project.</p></div>'}`;
    renderPresence();
    root().querySelectorAll('[data-board-action]').forEach((button) => { button.disabled = busy; });
  }
  function renderPresence() {
    const element = document.getElementById('board-presence');
    if (!element) return;
    const members = [...getMembers(), ...memberLookup];
    element.innerHTML = `<div class="presence-bar" role="status"><span class="badge">${esc(status)}</span>${handoffBoundary !== null ? `<span data-handoff-boundary="${handoffBoundary}">Replay boundary ${handoffBoundary}</span>` : ''}<span>${presence.length} online</span>${presence.map((person) => {
      const member = members.find((m) => m.user_id === person.user_id || m.id === person.user_id);
      const name = member?.email || member?.name || (person.user_id === getUserID() ? 'You' : 'Collaborator');
      const scene = scenes.find((s) => s.id === person.scene_id);
      return `<span class="presence-person"><span class="presence-avatar">${esc(name.slice(0, 2).toUpperCase())}</span>${esc(name)}${scene ? ` · ${esc(scene.title)}` : ''}</span>`;
    }).join('')}</div>${accessMessage ? `<p class="field-note" data-access-message role="status">${esc(accessMessage)}</p>` : ''}${connectionError ? `<div class="error-message" role="alert">${esc(connectionError)} Use Sync changes to retry replay.</div>` : ''}`;
  }
  function startSocket(current) {
    if (livePaused || socket || !current.board || current !== synchronizer) return;
    const id = projectID;
    socket = connectProjectSocket({
      projectId: id,
      getTicket: () => api.request('ws-ticket', { context: { projectID: id } }),
      lastSeenSeq: () => current.lastSeq,
      onStatus(next, error) {
        if (current !== synchronizer) return;
        status = next;
        if (error) connectionError = error.message;
        if (next !== 'online') presence = [];
        renderPresence();
      },
      async onMessage(message) {
        if (current !== synchronizer) return;
        if (message.type === 'replay_start') {
          if (!Number.isSafeInteger(message.boundary_seq) || message.boundary_seq < 0) throw new Error('Invalid replay boundary.');
          handoffBoundary = message.boundary_seq; renderPresence();
        } else if (message.type === 'replay_complete') {
          if (!Number.isSafeInteger(message.boundary_seq) || message.boundary_seq < 0 || message.boundary_seq !== handoffBoundary) throw new Error('Invalid replay completion boundary.');
          if (current.lastSeq < message.boundary_seq) await current.sync();
          if (current.lastSeq < message.boundary_seq) throw new Error('Replay boundary is not fully applied. Use Sync changes.');
        } else if (message.type === 'permissions') {
          accessMessage = message.message || `Project role: ${message.role}`;
          if (message.access_revoked) { presence = []; status = 'revoked'; }
          else if (message.role) onRoleChange(message.role);
          renderPresence();
        } else if (message.type === 'presence') {
          presence = (message.users || []).filter((person) => person.connected);
          renderPresence();
          if (presence.some((person) => ![...getMembers(), ...memberLookup].some((m) => m.user_id === person.user_id || m.id === person.user_id))) {
            const members = await api.request('project-members', { context: { projectID: id } });
            if (current !== synchronizer) return;
            memberLookup = Array.isArray(members) ? members : members.members || []; renderPresence();
          }
        } else if (message.type === 'operation' || message.type === 'replay') {
          await current.receive(message.type === 'operation' ? [message.operation] : message.operations || [], { hasMore: message.boundary_seq === undefined && Boolean(message.has_more) });
          if (current === synchronizer) { connectionError = ''; renderPresence(); }
        } else if (message.type === 'error') { throw new Error(message.message || 'WebSocket error.'); }
      },
    });
  }
  function showError(error) {
    if (!root()) return;
    const message = document.createElement('div'); message.className = 'error-message'; message.setAttribute('role', 'alert'); message.textContent = error.message;
    root().prepend(message);
  }
  async function load(id = projectID) {
    if (id !== projectID || !synchronizer) {
      socket?.close(); socket = null;
      synchronizer?.stop();
      projectID = id;
      synchronizer = createBoardSync({ api, projectID: id, clientID, storage, userID: getUserID(), onChange(board) { scenes = board.scenes; if (viewedScene && !scenes.some((scene) => scene.id === viewedScene)) { viewedScene = ''; socket?.viewScene(''); } render(); } });
    }
    const current = synchronizer;
    const requestRevision = ++revision;
    if (!root()) return;
    root().innerHTML = '<div class="loading-screen" role="status"><span class="spinner"></span> Synchronizing storyboard…</div>';
    try {
      if (!current.board) await current.initialize(); else await current.sync();
      if (current !== synchronizer || requestRevision !== revision || !root()) return;
      scenes = current.board.scenes; connectionError = ''; render(); startSocket(current);
    } catch (error) {
      if (current !== synchronizer || requestRevision !== revision || !root()) return;
      if (current.board) { scenes = current.board.scenes; render(); startSocket(current); }
      else root().innerHTML = action('reload', 'Retry loading storyboard');
      showError(error);
    }
  }
  function dispose() { livePaused = false; socket?.close(); socket = null; status = 'offline'; presence = []; viewedScene = ''; connectionError = ''; accessMessage = ''; handoffBoundary = null; memberLookup = []; synchronizer?.stop(); synchronizer = null; revision++; projectID = ''; scenes = []; busy = false; }
  async function write(type, payload, opID, expectedProjectID) {
    const current = synchronizer;
    if (!current || projectID !== expectedProjectID) throw new Error('Storyboard view changed.');
    try { await current.submit(type, payload, opID); }
    finally { if (current === synchronizer && current.board && root()) { scenes = current.board.scenes; render(); } }
  }
  const checkbox = (name, label) => `<label class="patch-option"><input type="checkbox" name="include-${name}">Update ${label}</label>`;
  function field(name, label, value = '', { multiline = false, patch = false } = {}) {
    const id = `board-field-${name}`;
    return `<div class="board-field">${patch ? checkbox(name, label) : ''}<label for="${id}">${label}</label>${multiline ? `<textarea id="${id}" name="${name}" rows="3">${esc(value)}</textarea>` : `<input id="${id}" type="text" name="${name}" value="${esc(value)}" ${name === 'position' ? 'inputmode="numeric"' : ''}>`}</div>`;
  }
  function watchPatch() {
    document.querySelectorAll('#modal-form .board-field input:not([type="checkbox"]), #modal-form .board-field textarea').forEach((input) => {
      input.addEventListener('input', () => { const checkbox = document.querySelector(`#modal-form [name="include-${input.name}"]`); if (checkbox) checkbox.checked = true; });
    });
  }
  function sceneForm(scene) {
    const requestContext = context(scene?.id);
    const patch = Boolean(scene);
    const opID = crypto.randomUUID();
    const sceneID = scene?.id || crypto.randomUUID();
    const fields = field('title', 'Title', scene?.title, { patch }) + field('description', 'Description', scene?.description, { multiline: true, patch });
    showForm(patch ? 'Edit scene' : 'Add scene', patch ? 'Changed fields are selected automatically. Uncheck a field to leave it unchanged.' : 'Give the next part of your story a title and description.', fields, patch ? 'Save scene' : 'Create scene', async (values) => {
      const body = {};
      for (const name of ['title', 'description']) {
        if (!patch || values[`include-${name}`]) body[name] = values[name];
      }
      await write(patch ? 'scene.update' : 'scene.create', { scene_id: sceneID, ...body }, opID, requestContext.projectID);
      closeModal(); toast(patch ? 'Scene updated.' : 'Scene created.');
    });
    if (patch) watchPatch();
  }
  function shotForm(scene, shot) {
    const requestContext = context(scene.id, shot?.id);
    const patch = Boolean(shot);
    const opID = crypto.randomUUID();
    const shotID = shot?.id || crypto.randomUUID();
    const fields = field('prompt', 'Prompt', shot?.prompt, { multiline: true, patch }) + field('style', 'Style', shot?.style, { patch }) + field('notes', 'Notes', shot?.notes, { multiline: true, patch }) + field('image_url', 'Image URL', shot?.image_url || '', { patch }) + (!patch ? '<label class="patch-option"><input type="checkbox" name="null-image" checked>No image (send null)</label>' : '<p class="field-note">Clear the Image URL field to remove its displayed image. A null update does not clear URLs in the current backend.</p>');
    showForm(patch ? 'Edit shot' : 'Add shot', patch ? 'Only selected fields are sent. Editing a field selects it automatically.' : `Add a shot to ${esc(scene.title)}.`, fields, patch ? 'Save shot' : 'Create shot', async (values) => {
      const body = {};
      for (const name of ['prompt', 'style', 'notes', 'image_url']) {
        if (!patch || values[`include-${name}`]) body[name] = name === 'image_url' && !patch && values['null-image'] ? null : values[name];
      }
      await write(patch ? 'shot.update' : 'shot.create', { scene_id: scene.id, shot_id: shotID, ...body }, opID, requestContext.projectID);
      closeModal(); toast(patch ? 'Shot updated.' : 'Shot created.');
    });
    if (patch) watchPatch();
    else document.querySelector('#modal-form [name="image_url"]').addEventListener('input', () => { document.querySelector('#modal-form [name="null-image"]').checked = false; });
  }
  function deleteForm(scene, shot) {
    const requestContext = context(scene.id, shot?.id);
    const opID = crypto.randomUUID();
    showForm(shot ? 'Delete this shot?' : 'Delete this scene?', shot ? 'This shot will be permanently removed.' : `“${esc(scene.title)}” and all ${scene.shots.length} of its shots will be permanently removed.`, '', shot ? 'Delete shot' : 'Delete scene', async () => {
      await write(shot ? 'shot.delete' : 'scene.delete', { scene_id: scene.id, ...(shot ? { shot_id: shot.id } : {}) }, opID, requestContext.projectID);
      closeModal(); toast(shot ? 'Shot deleted.' : 'Scene and its shots deleted.');
    });
    document.querySelector('#modal-form [type="submit"]').className = 'danger-button';
  }
  function orderForm(scene) {
    const requestContext = context(scene?.id);
    const items = scene ? scene.shots : scenes;
    const opID = crypto.randomUUID();
    showForm(scene ? 'Edit shot order' : 'Edit scene order', 'Each line is one ID in the desired order. The backend checks completeness, duplicates, and access.', `<p class="field-note">${items.map((item) => esc(item.title || item.prompt || 'Untitled')).join(' → ') || 'No items yet.'}</p>${field('ids', 'IDs in order (one per line)', items.map((item) => item.id).join('\n'), { multiline: true })}`, 'Save order', async ({ ids }) => {
      const orderedIDs = ids === '' ? [] : ids.split('\n');
      await write(scene ? 'shot.reorder' : 'scene.reorder', scene ? { scene_id: scene.id, shot_ids: orderedIDs } : { scene_ids: orderedIDs }, opID, requestContext.projectID);
      closeModal(); toast('Order saved.');
    });
  }
  async function move(scene, shot, direction) {
    const requestContext = context(shot ? scene.id : undefined);
    const items = shot ? scene.shots : scenes;
    const index = items.findIndex((item) => item.id === (shot || scene).id);
    const destination = index + direction;
    // At a list boundary the order is unchanged, but the request still tests permissions.
    const ids = items.map((item) => item.id);
    if (destination >= 0 && destination < ids.length) [ids[index], ids[destination]] = [ids[destination], ids[index]];
    await write(shot ? 'shot.reorder' : 'scene.reorder', shot ? { scene_id: scene.id, shot_ids: ids } : { scene_ids: ids }, crypto.randomUUID(), requestContext.projectID);
    toast('Order saved.');
  }
  async function handle(button) {
    if (busy) return;
    const name = button.dataset.boardAction;
    const scene = scenes.find((item) => item.id === button.dataset.scene);
    const shot = scene?.shots.find((item) => item.id === button.dataset.shot);
    if (scene) { viewedScene = scene.id; socket?.viewScene(scene.id); }
    if (name === 'view-scene') { render(); return; }
    if (name === 'toggle-live') {
      livePaused = !livePaused;
      if (livePaused) { socket?.close(); socket = null; status = 'offline'; presence = []; }
      else { connectionError = ''; startSocket(synchronizer); }
      render(); return;
    }
    if (name === 'create-scene') return sceneForm();
    if (name === 'edit-scene' && scene) return sceneForm(scene);
    if (name === 'create-shot' && scene) return shotForm(scene);
    if (name === 'edit-shot' && shot) return shotForm(scene, shot);
    if (name === 'delete-scene' && scene) return deleteForm(scene);
    if (name === 'delete-shot' && shot) return deleteForm(scene, shot);
    if (name === 'order-scenes') return orderForm();
    if (name === 'order-shots' && scene) return orderForm(scene);
    busy = true;
    root()?.querySelectorAll('[data-board-action]').forEach((button) => { button.disabled = true; });
    const actionRevision = revision;
    try {
      if (name === 'reload') await load();
      else if (name === 'retry-write') { await synchronizer.retry(); scenes = synchronizer.board.scenes; render(); toast('Pending write confirmed and replayed.'); }
      else if (name.endsWith('-up') || name.endsWith('-down')) await move(scene, shot, name.endsWith('-up') ? -1 : 1);
    } catch (error) {
      if (actionRevision === revision && root()) {
        render(); const message = document.createElement('div'); message.className = 'error-message'; message.setAttribute('role', 'alert'); message.textContent = error.message; root().prepend(message);
      }
    } finally {
      busy = false;
      root()?.querySelectorAll('[data-board-action]').forEach((button) => { button.disabled = false; });
    }
  }
  return { load, dispose, handle };
}
