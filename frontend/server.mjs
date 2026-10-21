import http from 'node:http';
import { readFile } from 'node:fs/promises';
import { fileURLToPath } from 'node:url';
import { resolve } from 'node:path';

const root = fileURLToPath(new URL('./', import.meta.url));
const assets = {
  '/': ['index.html', 'text/html; charset=utf-8'],
  '/app.js': ['app.js', 'text/javascript; charset=utf-8'],
  '/api.js': ['api.js', 'text/javascript; charset=utf-8'],
  '/endpoints.js': ['endpoints.js', 'text/javascript; charset=utf-8'],
  '/styles.css': ['styles.css', 'text/css; charset=utf-8'],
};

export function createServer(target = process.env.API_TARGET || 'http://localhost:8080') {
  const upstream = new URL(target);
  if (!['http:', 'https:'].includes(upstream.protocol)) throw new Error('API_TARGET must use HTTP or HTTPS');
  return http.createServer(async (req, res) => {
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
}

if (process.argv[1] && resolve(process.argv[1]) === resolve(root, 'server.mjs')) {
  const port = Number(process.env.FRONTEND_PORT || 5173);
  createServer().listen(port, process.env.FRONTEND_HOST || '127.0.0.1', () => {
    console.log(`MultiWorks: http://localhost:${port}`);
    console.log(`Backend: ${process.env.API_TARGET || 'http://localhost:8080'}`);
  });
}
