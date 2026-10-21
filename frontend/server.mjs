import http from 'node:http';
import https from 'node:https';
import { readFile } from 'node:fs/promises';
import { fileURLToPath } from 'node:url';
import { resolve } from 'node:path';

const root = fileURLToPath(new URL('./', import.meta.url));
const assets = {
  '/': ['index.html', 'text/html; charset=utf-8'],
  '/app.js': ['app.js', 'text/javascript; charset=utf-8'],
  '/api.js': ['api.js', 'text/javascript; charset=utf-8'],
  '/projectSocket.js': ['projectSocket.js', 'text/javascript; charset=utf-8'],
  '/board-sync.js': ['board-sync.js', 'text/javascript; charset=utf-8'],
  '/stage8.js': ['stage8.js', 'text/javascript; charset=utf-8'],
  '/storyboard.js': ['storyboard.js', 'text/javascript; charset=utf-8'],
  '/endpoints.js': ['endpoints.js', 'text/javascript; charset=utf-8'],
  '/styles.css': ['styles.css', 'text/css; charset=utf-8'],
};

export function createServer(target = process.env.API_TARGET || 'http://localhost:8080') {
  const upstream = new URL(target);
  if (!['http:', 'https:'].includes(upstream.protocol)) throw new Error('API_TARGET must use HTTP or HTTPS');
  const server = http.createServer(async (req, res) => {
    const path = new URL(req.url, 'http://localhost').pathname;
    if (path.startsWith('/backend/')) {
      try {
        // Keep every request on the configured backend, including paths beginning with //.
        const url = new URL(upstream);
        url.pathname = path.slice('/backend'.length);
        url.search = new URL(req.url, 'http://localhost').search;
        const headers = {};
        for (const key of ['authorization', 'content-type', 'accept']) {
          if (req.headers[key]) headers[key] = req.headers[key];
        }
        const response = await fetch(url, {
          method: req.method, headers,
          ...(!['GET', 'HEAD'].includes(req.method) ? { body: req, duplex: 'half' } : {}),
          redirect: 'manual', signal: AbortSignal.timeout(30000),
        });
        res.writeHead(response.status, {
          'content-type': response.headers.get('content-type') || 'text/plain',
          'cache-control': 'no-store',
          'x-content-type-options': 'nosniff',
        });
        res.end(Buffer.from(await response.arrayBuffer()));
      } catch {
        res.writeHead(502, { 'content-type': 'application/json' });
        res.end(JSON.stringify({ error: `Cannot reach the API at ${upstream.origin}. Start the backend or set API_TARGET and restart the frontend.` }));
      }
      return;
    }
    if (path === '/config' && req.method === 'GET') {
      res.writeHead(200, { 'content-type': 'application/json', 'cache-control': 'no-store' });
      res.end(JSON.stringify({ target: upstream.origin }));
      return;
    }
    const asset = assets[path];
    if (!asset || !['GET', 'HEAD'].includes(req.method)) {
      res.writeHead(404); res.end('Not found'); return;
    }
    try {
      const content = await readFile(new URL(asset[0], import.meta.url));
      res.writeHead(200, { 'content-type': asset[1], 'cache-control': 'no-store' });
      res.end(req.method === 'HEAD' ? undefined : content);
    } catch {
      res.writeHead(500); res.end('Could not load frontend asset');
    }
  });
  server.on('upgrade', (req, socket, head) => {
    const incoming = new URL(req.url, 'http://localhost');
    if (!/^\/backend\/api\/projects\/[^/]+\/ws$/.test(incoming.pathname)) { socket.end('HTTP/1.1 404 Not Found\r\nConnection: close\r\n\r\n'); return; }
    const url = new URL(upstream);
    url.pathname = incoming.pathname.slice('/backend'.length); url.search = incoming.search;
    const headers = { ...req.headers, host: url.host };
    delete headers.authorization;
    const request = (url.protocol === 'https:' ? https : http).request(url, { method: 'GET', headers });
    request.setTimeout(10000, () => request.destroy());
    request.on('upgrade', (response, peer, upstreamHead) => {
      request.setTimeout(0);
      socket.write(`HTTP/1.1 101 Switching Protocols\r\n${Object.entries(response.headers).map(([key, value]) => `${key}: ${value}`).join('\r\n')}\r\n\r\n`);
      if (upstreamHead.length) socket.write(upstreamHead);
      if (head.length) peer.write(head);
      socket.on('error', () => peer.destroy()); peer.on('error', () => socket.destroy());
      socket.on('close', () => peer.destroy()); peer.on('close', () => socket.destroy());
      socket.pipe(peer); peer.pipe(socket);
    });
    request.on('response', (response) => {
      const chunks = [];
      response.on('data', (chunk) => chunks.push(chunk));
      response.on('end', () => { const body = Buffer.concat(chunks); socket.end(`HTTP/1.1 ${response.statusCode} Rejected\r\nConnection: close\r\nContent-Length: ${body.length}\r\n\r\n${body}`); });
    });
    request.on('error', () => socket.destroy());
    socket.on('close', () => request.destroy());
    request.end();
  });
  return server;
}

if (process.argv[1] && resolve(process.argv[1]) === resolve(root, 'server.mjs')) {
  const port = Number(process.env.FRONTEND_PORT || 5173);
  createServer().listen(port, process.env.FRONTEND_HOST || '127.0.0.1', () => {
    console.log(`MultiWorks: http://localhost:${port}`);
    console.log(`Backend: ${process.env.API_TARGET || 'http://localhost:8080'}`);
  });
}
