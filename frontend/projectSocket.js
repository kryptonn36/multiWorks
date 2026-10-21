// Each connection attempt obtains a new, single-use ticket via authenticated HTTP.
export function connectProjectSocket({ projectId, getTicket, lastSeenSeq, onMessage, onStatus, WebSocketClass = globalThis.WebSocket, location = globalThis.location, timers = globalThis }) {
  let socket, reconnectTimer, heartbeatTimer;
  let closed = false;
  let attempts = 0;
  let sceneId = '';
  const sendScene = () => {
    if (socket?.readyState === WebSocketClass.OPEN) socket.send(JSON.stringify({ type: 'view_scene', scene_id: sceneId }));
  };
  function retry() {
    if (closed) return;
    onStatus('offline');
    reconnectTimer = timers.setTimeout(connect, Math.min(1000 * 2 ** attempts++, 15000));
  }
  async function connect() {
    if (closed) return;
    onStatus('connecting');
    try {
      const { ticket } = await getTicket();
      if (closed) return;
      if (typeof ticket !== 'string' || !ticket) throw new Error('Missing WebSocket ticket.');
      const url = new URL(`/backend/api/projects/${encodeURIComponent(projectId)}/ws`, location.href);
      url.protocol = location.protocol === 'https:' ? 'wss:' : 'ws:';
      url.searchParams.set('ticket', ticket);
      url.searchParams.set('last_seen_seq', String(lastSeenSeq()));
      socket = new WebSocketClass(url);
      socket.onopen = () => {
        if (closed) { socket.close(); return; }
        attempts = 0; onStatus('syncing'); sendScene();
        heartbeatTimer = timers.setInterval(() => {
          if (socket?.readyState === WebSocketClass.OPEN) socket.send(JSON.stringify({ type: 'heartbeat' }));
        }, 15000);
      };
      const connection = socket;
      let delivery = Promise.resolve();
      socket.onmessage = (event) => {
        let message;
        try { message = JSON.parse(event.data); }
        catch (error) { if (!closed) onStatus('error', error); return; }
        if (closed || socket !== connection) return;
        // Permission notifications must not wait behind a slow HTTP gap replay.
        if (message.type === 'permissions') {
          if (message.access_revoked) {
            closed = true; timers.clearTimeout(reconnectTimer); timers.clearInterval(heartbeatTimer);
            onStatus('revoked'); connection.close();
          }
          Promise.resolve(onMessage(message)).catch((error) => onStatus('error', error));
          return;
        }
        delivery = delivery.then(async () => {
          if (closed || socket !== connection) return;
          if (message.type === 'replay_start') onStatus('syncing');
          await onMessage(message);
          if (closed || socket !== connection || connection.readyState !== WebSocketClass.OPEN) return;
          if (message.type === 'replay_complete') onStatus('online');
        }).catch((error) => { if (!closed && socket === connection) onStatus('error', error); });
      };
      socket.onerror = () => { if (!closed) onStatus('error'); };
      socket.onclose = (event) => {
        timers.clearInterval(heartbeatTimer);
        if (event?.code === 1008 && !closed) { closed = true; onStatus('revoked', new Error('Project access was revoked.')); return; }
        retry();
      };
    } catch (error) { if (!closed) {
      if ([403, 404].includes(error.status)) { closed = true; onStatus('revoked', error); return; }
      onStatus('error', error); retry();
    } }
  }
  connect();
  return {
    viewScene(id) { sceneId = id || ''; sendScene(); },
    close() {
      closed = true; timers.clearTimeout(reconnectTimer); timers.clearInterval(heartbeatTimer); socket?.close();
    },
  };
}
