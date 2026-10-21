import { spawn } from 'node:child_process';
import { mkdtemp, rm } from 'node:fs/promises';

export async function launchBrowser() {
  if (typeof WebSocket !== 'function') throw new Error('Browser checks require Node 22 or later.');
  const profile = await mkdtemp('/tmp/multiworks-stage9-browser-');
  const child = spawn(process.env.CHROME_BIN || '/usr/bin/google-chrome', ['--headless', '--no-sandbox', '--disable-gpu', '--disable-dev-shm-usage', '--no-first-run', '--no-default-browser-check', '--remote-debugging-port=0', `--user-data-dir=${profile}`, 'about:blank'], { stdio: ['ignore', 'ignore', 'pipe'] });
  const browserURL = await new Promise((resolve, reject) => {
    let output = '';
    const timer = setTimeout(() => reject(new Error('Chrome did not start; set CHROME_BIN to Chrome/Chromium.')), 15000);
    child.once('error', error => { clearTimeout(timer); reject(error); });
    child.once('exit', code => { clearTimeout(timer); reject(new Error(`Chrome exited ${code}`)); });
    child.stderr.on('data', chunk => {
      output += chunk;
      const match = output.match(/DevTools listening on (ws:\/\/[^\s]+)/);
      if (match) { clearTimeout(timer); resolve(match[1]); }
    });
  });
  const debugBase = `http://${new URL(browserURL).host}`;
  const control = await Page.attach(browserURL, false);
  const pages = [];
  return {
    async newPage(base) {
      // Each participant gets an isolated Chrome browser context (cookies,
      // session storage and credentials are separate, like separate profiles).
      const { browserContextId } = await control.call('Target.createBrowserContext');
      const { targetId } = await control.call('Target.createTarget', { url: 'about:blank', browserContextId });
      const targets = await (await fetch(`${debugBase}/json/list`)).json();
      const target = targets.find(item => item.id === targetId);
      if (!target) throw new Error('Chrome did not create the test page.');
      const page = await Page.attach(target.webSocketDebuggerUrl);
      page.base = base; pages.push(page); return page;
    },
    async close() {
      for (const page of pages) page.socket.close();
      control.socket.close();
      if (child.exitCode === null) {
        const exited = new Promise(resolve => child.once('exit', resolve));
        child.kill('SIGTERM'); await exited;
      }
      await rm(profile, { recursive: true, force: true, maxRetries: 10, retryDelay: 200 });
    },
  };
}
class Page {
  static async attach(url, enable = true) {
    const page = new Page(); page.sequence = 0; page.pending = new Map(); page.errors = [];
    page.socket = new WebSocket(url);
    page.socket.addEventListener('message', ({ data }) => {
      const message = JSON.parse(data);
      if (message.id) {
        const pending = page.pending.get(message.id); if (!pending) return;
        page.pending.delete(message.id); clearTimeout(pending.timer);
        message.error ? pending.reject(new Error(message.error.message)) : pending.resolve(message.result);
      }
      if (message.method === 'Runtime.exceptionThrown') page.errors.push(message.params.exceptionDetails.exception?.description || message.params.exceptionDetails.text);
    });
    await new Promise((resolve, reject) => { page.socket.addEventListener('open', resolve, { once: true }); page.socket.addEventListener('error', reject, { once: true }); });
    if (enable) { await page.call('Runtime.enable'); await page.call('Page.enable'); }
    return page;
  }
  call(method, params = {}) {
    return new Promise((resolve, reject) => {
      const id = ++this.sequence;
      const timer = setTimeout(() => { this.pending.delete(id); reject(new Error(`Chrome command timed out: ${method}`)); }, 10000);
      this.pending.set(id, { resolve, reject, timer }); this.socket.send(JSON.stringify({ id, method, params }));
    });
  }
  async evaluate(expression) {
    const result = await this.call('Runtime.evaluate', { expression, returnByValue: true, awaitPromise: true });
    if (result.exceptionDetails) throw new Error(result.exceptionDetails.exception?.description || result.exceptionDetails.text);
    return result.result?.value;
  }
  async wait(expression, timeout = 45000) {
    const end = Date.now() + timeout;
    while (Date.now() < end) { if (await this.evaluate(expression)) return; await new Promise(resolve => setTimeout(resolve, 100)); }
    throw new Error(`Browser check timed out: ${expression}\n${await this.evaluate('document.body.innerText')}`);
  }
  async navigate(path) { await this.call('Page.navigate', { url: `${this.base}/${path}` }); await this.wait('document.readyState === "complete"'); }
  async click(selector) { await this.wait(`!!document.querySelector(${JSON.stringify(selector)})`); await this.evaluate(`document.querySelector(${JSON.stringify(selector)}).click()`); }
  async fill(selector, value) { await this.evaluate(`{const input=document.querySelector(${JSON.stringify(selector)});input.value=${JSON.stringify(value)};input.dispatchEvent(new Event('input',{bubbles:true}));}`); }
  async submit() { await this.evaluate('document.querySelector("#modal-form").requestSubmit()'); }
}
