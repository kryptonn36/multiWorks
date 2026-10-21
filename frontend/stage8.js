// Asset bytes always use the authenticated API client; URLs never carry JWTs.
export function createStage8({ api, escape: esc, showForm, closeModal, toast, onChange, sync, getSequence = () => 0 }) {
  let projectID = '';
  let generation = 0;
  let loading;
  let error = '';
  let proposalTimer;
  let checkingProposals = false;
  const assets = new Map();
  const proposals = new Map();
  const thumbnails = new Map();
  const fetching = new Set();
  const imageErrors = new Map();
  const button = (action, label, attributes = '') => `<button type="button" class="secondary" data-board-action="${action}" ${attributes}>${label}</button>`;
  const active = (id, version) => id === projectID && version === generation;
  function dispose() {
    clearTimeout(proposalTimer); proposalTimer = undefined; checkingProposals = false;
    generation++; projectID = ''; loading = undefined; error = '';
    for (const url of thumbnails.values()) URL.revokeObjectURL(url);
    assets.clear(); proposals.clear(); thumbnails.clear(); fetching.clear(); imageErrors.clear();
  }
  async function thumbnail(asset) {
    if (asset.status !== 'ready' || thumbnails.has(asset.id) || fetching.has(asset.id) || imageErrors.has(asset.id)) return;
    const id = projectID; const version = generation;
    fetching.add(asset.id);
    try {
      const blob = await api.request('asset-thumbnail', { context: { projectID: id, assetID: asset.id }, responseType: 'blob' });
      if (active(id, version)) thumbnails.set(asset.id, URL.createObjectURL(blob));
    } catch (failure) { if (active(id, version)) imageErrors.set(asset.id, failure.message); }
    finally { if (active(id, version)) { fetching.delete(asset.id); onChange(); } }
  }
  function mergeAsset(asset) {
    if (!asset || asset.project_id !== projectID) return;
    const previous = assets.get(asset.id);
    // Processing responses can race the worker's ready event.
    if (previous && Date.parse(previous.updated_at) > Date.parse(asset.updated_at)) return;
    if (previous?.status !== 'processing' && asset.status === 'processing' && previous) return;
    assets.set(asset.id, asset); thumbnail(asset);
  }
  function mergeProposal(proposal) {
    if (!proposal || proposal.project_id !== projectID) return;
    const previous = proposals.get(proposal.id);
    if (previous && Date.parse(previous.updated_at) > Date.parse(proposal.updated_at)) return;
    const rank = { generating: 0, pending: 1, approved: 2, rejected: 2, stale: 2, failed: 2 };
    if (previous && rank[previous.status] > rank[proposal.status]) return;
    proposals.set(proposal.id, proposal);
    scheduleProposalCheck();
  }
  // Poll only unfinished generation jobs as a fallback for missed/offline events.
  function scheduleProposalCheck() {
    if (proposalTimer || checkingProposals || ![...proposals.values()].some(p => p.status === 'generating')) return;
    const id = projectID; const version = generation;
    proposalTimer = setTimeout(async () => {
      proposalTimer = undefined; checkingProposals = true;
      try {
        for (const proposal of [...proposals.values()].filter(p => p.status === 'generating')) {
          const item = await api.request('proposal', { context: { projectID: id, proposalID: proposal.id } });
          if (!active(id, version)) return;
          mergeProposal(item);
        }
        if (active(id, version)) error = '';
      } catch (failure) { if (active(id, version)) error = failure.message; }
      finally { if (active(id, version)) { checkingProposals = false; onChange(); scheduleProposalCheck(); } }
    }, 1500);
  }
  async function pages(endpoint, key, merge, id, version) {
    let after;
    do {
      const data = await api.request(endpoint, { context: { projectID: id }, query: after ? { after_id: after } : undefined });
      if (!active(id, version)) return;
      const items = data[key] || [];
      items.forEach(merge);
      after = data.has_more && items.length ? items.at(-1).id : undefined;
    } while (after);
  }
  function load(id = projectID) {
    if (id !== projectID) { dispose(); projectID = id; }
    if (!id) return Promise.resolve();
    if (loading) return loading;
    const version = generation; error = '';
    loading = Promise.all([
      pages('assets', 'assets', mergeAsset, id, version),
      pages('proposals', 'proposals', mergeProposal, id, version),
    ]).catch((failure) => { if (active(id, version)) error = failure.message; }).finally(() => {
      if (active(id, version)) { loading = undefined; onChange(); }
    });
    return loading;
  }
  function message(message) {
    if (message.type === 'asset_status') mergeAsset(message.asset);
    else if (message.type === 'agent_proposal') mergeProposal(message.proposal);
    else return;
    onChange();
  }
  function shotAssets(shot) {
    return `<div class="shot-assets"><h5>Uploaded images</h5>${(shot.asset_ids || []).map((id) => {
      const asset = assets.get(id); const url = thumbnails.get(id);
      return `<div class="asset-card" data-asset-id="${esc(id)}">${url ? `<img class="asset-thumbnail" src="${esc(url)}" alt="Uploaded shot image">` : ''}<span class="badge" data-asset-status="${esc(asset?.status || 'loading')}">${esc(asset?.status || 'Loading image…')}</span>${asset ? `<span class="small muted">${esc(asset.mime_type)} · ${(asset.size_bytes / 1024).toFixed(1)} KB</span>` : ''}${asset?.error_message ? `<p class="error-message">${esc(asset.error_message)}</p>` : ''}${imageErrors.has(id) ? `<p class="error-message">${esc(imageErrors.get(id))}</p>` : ''}${button('download-asset', 'Download original', `data-asset="${esc(id)}"`)}</div>`;
    }).join('')}${button('upload-asset', 'Upload image', `data-shot="${esc(shot.id)}"`)}</div>`;
  }
  function panel() {
    const items = [...proposals.values()].sort((a, b) => Date.parse(b.created_at) - Date.parse(a.created_at));
    return `<section class="panel agent-panel" aria-label="Agent co-editor"><div class="section-heading"><div><h2>Agent co-editor</h2><p>A local mock proposes edits. Review and approve before they change the board.</p></div><div class="heading-actions">${button('refresh-stage8', 'Refresh assets & proposals')}${button('create-proposal', 'New proposal')}</div></div>${error ? `<div class="error-message" role="alert">${esc(error)}</div>` : ''}${loading ? '<p role="status">Loading assets and proposals…</p>' : ''}${items.map((p) => `<article class="proposal-card" data-proposal-id="${esc(p.id)}"><div class="section-heading"><strong>${esc(p.instruction)}</strong><span class="badge" data-proposal-status="${esc(p.status)}">${esc(p.status)}</span></div><p class="small muted">Mock: ${esc(p.mode)} · Based on version ${esc(p.base_seq)} · ${esc(p.created_at)}</p>${p.status === 'generating' ? '<p role="status"><span class="spinner"></span> Generating proposal…</p>' : ''}${['pending', 'generating'].includes(p.status) && p.base_seq !== getSequence() ? '<p class="error-message" data-proposal-warning role="status">The board changed after this proposal was requested. Generate a fresh proposal before approval.</p>' : ''}${p.failure_reason ? `<p class="error-message">${esc(p.failure_reason)}</p>` : ''}<ol>${(p.proposed_ops || p.operations || []).map((op) => `<li><strong>${esc({ 'scene.create': 'Create scene', 'scene.update': 'Edit scene', 'scene.delete': 'Delete scene', 'scene.reorder': 'Reorder scenes', 'shot.create': 'Create shot', 'shot.update': 'Edit shot', 'shot.delete': 'Delete shot', 'shot.reorder': 'Reorder shots' }[op.type] || op.type)}</strong>${Object.entries(op.payload).map(([key, value]) => `<p><span class="muted">${esc(key.replaceAll('_', ' '))}:</span> ${esc(Array.isArray(value) ? value.join(' → ') : value)}</p>`).join('')}</li>`).join('')}</ol><div class="heading-actions">${button('approve-proposal', 'Approve', `data-proposal="${esc(p.id)}"`)}${button('reject-proposal', 'Reject', `data-proposal="${esc(p.id)}"`)}</div></article>`).join('') || '<p class="board-empty">No proposals yet.</p>'}</section>`;
  }
  async function handle(element) {
    const action = element.dataset.boardAction;
    if (!['upload-asset', 'download-asset', 'refresh-stage8', 'create-proposal', 'approve-proposal', 'reject-proposal'].includes(action)) return false;
    const id = projectID; const version = generation;
    const context = { projectID: id };
    const changed = () => { if (!active(id, version)) throw new Error('Project view changed.'); };
    if (action === 'refresh-stage8') { imageErrors.clear(); await load(); return true; }
    if (action === 'upload-asset') {
      showForm('Upload a shot image', 'Choose a file. The backend checks image format, size, dimensions, and permissions.', '<label>Image file<input type="file" name="file"></label>', 'Upload image', async ({ file }) => {
        changed(); const form = new FormData(); form.append('file', file);
        const result = await api.request('upload-asset', { context: { ...context, shotID: element.dataset.shot }, form });
        changed(); mergeAsset(result.asset); onChange(); await sync(); changed(); closeModal();
        toast(result.deduped ? 'Image attached. The existing stored file was reused.' : 'Image uploaded. Thumbnail processing has started.');
      });
    } else if (action === 'download-asset') {
      element.disabled = true;
      try {
        const blob = await api.request('asset-download', { context: { ...context, assetID: element.dataset.asset }, responseType: 'blob' });
        changed(); const url = URL.createObjectURL(blob); const link = document.createElement('a');
        link.href = url; link.download = `shot-image-${element.dataset.asset}`; link.click();
        setTimeout(() => URL.revokeObjectURL(url), 1000);
      } catch (failure) { if (active(id, version)) { error = failure.message; onChange(); toast(failure.message); } }
      finally { element.disabled = false; }
    } else if (action === 'create-proposal') {
      showForm('Ask the mock agent', 'Ask to add/create a shot or scene, for example: Add a close-up shot of a mysterious letter. Nothing is applied until approval.', '<label>Instruction<textarea name="instruction" rows="4"></textarea></label><label>Mock response mode<input type="text" name="mode" value="normal" list="agent-modes"><datalist id="agent-modes"><option value="normal"><option value="malformed"><option value="invalid"><option value="oversized"><option value="delayed"><option value="timeout"><option value="missing-shot"><option value="too-many"></datalist></label>', 'Generate proposal', async (body) => {
        changed(); const proposal = await api.request('create-proposal', { context, body }); changed();
        mergeProposal(proposal); onChange(); closeModal(); toast(proposal.status === 'failed' ? proposal.failure_reason : proposal.status === 'generating' ? 'Proposal generation started.' : 'Proposal ready for review.');
      });
    } else {
      const approve = action === 'approve-proposal';
      const proposalID = element.dataset.proposal;
      showForm(approve ? 'Approve this proposal?' : 'Reject this proposal?', approve ? 'All proposed edits are applied together. A changed board requires a fresh proposal.' : 'Rejecting leaves the board unchanged.', '', approve ? 'Approve edits' : 'Reject proposal', async () => {
        changed();
        try {
          const proposal = await api.request(action, { context: { ...context, proposalID }, body: {} }); changed(); mergeProposal(proposal); onChange();
          if (approve) await sync(); changed(); closeModal(); toast(approve ? 'Proposal applied.' : 'Proposal rejected.');
        } catch (failure) {
          if (active(id, version)) {
            try { const proposal = await api.request('proposal', { context: { ...context, proposalID } }); if (active(id, version)) { mergeProposal(proposal); onChange(); } } catch { /* Keep the original decision error. */ }
          }
          throw failure;
        }
      });
    }
    return true;
  }
  return { load, dispose, message, shotAssets, panel, handle };
}
