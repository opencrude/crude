#!/usr/bin/env node
/**
 * CSP frame-ancestors verification for neoma.bimehma.com:8443
 * ============================================================
 *
 * Verifies that https://clmtestb.bimehma.com (matches *.bimehma.com in CSP)
 * can embed https://neoma.bimehma.com:8443/ and that the /chat iframe
 * inside it loads successfully.
 *
 * PREREQUISITES:
 *   1. Node.js 18+ (uses built-in WebSocket + fetch)
 *   2. Google Chrome installed at the default Windows path
 *      (edit CHROME_PATH below if different)
 *   3. SSH tunnel to neoma:8443 active on 127.0.0.1:8443, e.g.:
 *        ssh -L 8443:127.0.0.1:8443 user@server
 *
 * USAGE:
 *   node verify-csp.js
 *
 * OUTPUT:
 *   Prints PASS or FAIL with details. Exit code 0 = pass, 1 = fail.
 */

const https = require('https');
const http = require('http');
const fs = require('fs');
const path = require('path');
const os = require('os');
const { spawn } = require('child_process');

// ── Config ──────────────────────────────────────────────────────────────
const CHROME_PATH =
  fs.existsSync('C:\\Program Files\\Google\\Chrome\\Application\\chrome.exe')
    ? 'C:\\Program Files\\Google\\Chrome\\Application\\chrome.exe'
    : fs.existsSync('/usr/bin/google-chrome')
      ? '/usr/bin/google-chrome'
      : 'chrome';

const TARGET = 'https://neoma.bimehma.com:8443/';
const PARENT_HOST = 'clmtestb.bimehma.com';
const CERT_DIR = path.join(os.tmpdir(), 'csp-verify-certs');
const CDP_PORT = 9661;

// ── Helpers ─────────────────────────────────────────────────────────────
function log(msg) { console.log(msg); }

function cdpCall(ws, method, params = {}, id) {
  return new Promise((resolve, reject) => {
    const handler = (ev) => {
      const data = JSON.parse(ev.data);
      if (data.id === id) {
        ws.removeEventListener('message', handler);
        if (data.error) reject(new Error(JSON.stringify(data.error)));
        else resolve(data.result);
      }
    };
    ws.addEventListener('message', handler);
    ws.send(JSON.stringify({ id, method, params }));
  });
}
let _cdpId = 0;
const nextCdpId = () => ++_cdpId;

// ── Generate self-signed cert for clmtestb.bimehma.com ──────────────────
function generateCert() {
  fs.mkdirSync(CERT_DIR, { recursive: true });
  const keyPath = path.join(CERT_DIR, 'key.pem');
  const certPath = path.join(CERT_DIR, 'cert.pem');
  if (fs.existsSync(keyPath) && fs.existsSync(certPath)) {
    log('[i] Reusing existing self-signed cert');
    return { keyPath, certPath };
  }
  const cnfPath = path.join(CERT_DIR, 'openssl.cnf');
  const cnf = `[req]
distinguished_name = dn
req_extensions = v3_req
prompt = no
[dn]
CN = ${PARENT_HOST}
[v3_req]
subjectAltName = DNS:${PARENT_HOST}
`;
  fs.writeFileSync(cnfPath, cnf);

  const opensslPaths = [
    'openssl',
    'C:\\Strawberry\\c\\bin\\openssl.exe',
    '/usr/bin/openssl',
  ];
  const env = { ...process.env, OPENSSL_CONF: cnfPath };
  for (const exe of opensslPaths) {
    try {
      const { execSync } = require('child_process');
      execSync(
        `"${exe}" req -x509 -newkey rsa:2048 -nodes -keyout "${keyPath}" -out "${certPath}" -days 1 -config "${cnfPath}" -extensions v3_req`,
        { env, stdio: 'pipe' }
      );
      log('[i] Generated self-signed cert for ' + PARENT_HOST);
      return { keyPath, certPath };
    } catch { /* try next */ }
  }
  throw new Error('Could not generate cert: openssl not found');
}

// ── Main ───────────────────────────────────────────────────────────────
async function main() {
  log('');
  log('========================================');
  log('  CSP frame-ancestors verification');
  log('========================================');
  log('');

  // 0. Check the target is reachable
  log('[1/5] Checking target ' + TARGET + ' ...');
  try {
    await new Promise((resolve, reject) => {
      const req = https.request(
        { hostname: '127.0.0.1', port: 8443, path: '/', method: 'GET', rejectUnauthorized: false },
        (res) => { res.resume(); resolve(); }
      );
      req.on('error', reject);
      req.setTimeout(5000, () => req.destroy(new Error('timeout')));
      req.end();
    });
    log('      Target reachable on 127.0.0.1:8443');
  } catch (e) {
    log('      ERROR: cannot reach 127.0.0.1:8443 — is the SSH tunnel up?');
    log('      ' + e.message);
    process.exit(1);
  }

  // 1. Check CSP header on target
  log('[2/5] Checking CSP header on target ...');
  const targetHeaders = await new Promise((resolve) => {
    const req = https.request(
      { hostname: '127.0.0.1', port: 8443, path: '/', method: 'GET', rejectUnauthorized: false },
      (res) => {
        const csp = res.headers['content-security-policy'] || '';
        const xfo = res.headers['x-frame-options'] || '';
        resolve({ csp, xfo });
        res.resume();
      }
    );
    req.on('error', () => resolve({ csp: '', xfo: '' }));
    req.end();
  });
  log('      CSP: ' + (targetHeaders.csp || '(none)'));
  log('      XFO: ' + (targetHeaders.xfo || '(none)'));
  if (!/frame-ancestors/.test(targetHeaders.csp)) {
    log('      WARNING: no frame-ancestors in CSP');
  }

  // 2. Generate cert
  log('[3/5] Generating self-signed cert for ' + PARENT_HOST + ' ...');
  const { keyPath, certPath } = generateCert();

  // 3. Serve parent page on HTTPS :443
  log('[4/5] Launching test: parent=https://' + PARENT_HOST + ' -> iframe=' + TARGET);
  const parentHtml = `<!doctype html><html><head><meta charset="utf-8">
<title>CSP verify</title></head><body>
<h3>Parent: ${PARENT_HOST}</h3>
<iframe id="f" src="${TARGET}" width="800" height="600"></iframe>
<script>console.log('PARENT origin=' + location.origin);</script>
</body></html>`;

  const server = https.createServer(
    { key: fs.readFileSync(keyPath), cert: fs.readFileSync(certPath) },
    (req, res) => {
      res.writeHead(200, { 'Content-Type': 'text/html; charset=utf-8' });
      res.end(parentHtml);
    }
  );

  await new Promise((resolve, reject) => {
    server.on('error', reject);
    server.listen(443, '127.0.0.1', resolve);
  });

  // 4. Launch Chrome and inspect frame tree
  const profile = fs.mkdtempSync(path.join(os.tmpdir(), 'csp-verify-'));
  const rules = `MAP ${PARENT_HOST} 127.0.0.1, MAP neoma.bimehma.com 127.0.0.1`;
  const chromeArgs = [
    '--headless=new',
    '--disable-gpu',
    '--no-first-run',
    '--no-default-browser-check',
    `--remote-debugging-port=${CDP_PORT}`,
    `--host-resolver-rules=${rules}`,
    `--user-data-dir=${profile}`,
    '--ignore-certificate-errors',
    'about:blank',
  ];
  const proc = spawn(CHROME_PATH, chromeArgs, { stdio: ['ignore', 'ignore', 'ignore'] });
  await new Promise((r) => setTimeout(r, 2000));

  let verdict = 'FAIL';
  let detail = '';
  try {
    const resp = await fetch(`http://127.0.0.1:${CDP_PORT}/json/list`);
    const tabs = await resp.json();
    const tab = tabs.find((t) => t.type === 'page');
    const ws = new WebSocket(tab.webSocketDebuggerUrl);
    await new Promise((res, rej) => {
      ws.addEventListener('open', res);
      ws.addEventListener('error', rej);
    });

    await cdpCall(ws, 'Page.enable', {}, nextCdpId());

    const violations = [];
    ws.addEventListener('message', (ev) => {
      const data = JSON.parse(ev.data);
      if (data.method === 'Log.entryAdded') {
        if (/frame-ancestors|violates the following/i.test(data.params.entry.text)) {
          violations.push(data.params.entry.text.slice(0, 200));
        }
      }
    });

    await cdpCall(ws, 'Page.navigate', { url: `https://${PARENT_HOST}/` }, nextCdpId());
    await new Promise((r) => setTimeout(r, 7000));

    const tree = await cdpCall(ws, 'Page.getFrameTree', {}, nextCdpId());
    const frames = [];
    function walk(node, depth) {
      frames.push({
        depth,
        url: node.frame.url,
        origin: node.frame.securityOrigin || '',
        mime: node.frame.mimeType || '',
      });
      for (const c of node.childFrames || []) walk(c, depth + 1);
    }
    walk(tree.frameTree, 0);

    log('');
    log('      Frame tree:');
    for (const f of frames) {
      const indent = '        ' + '  '.repeat(f.depth);
      const status = f.url.startsWith('chrome-error') ? ' BLOCKED' : ' OK';
      log(`${indent}[${f.depth}] ${f.url.slice(0, 95)}${status}`);
    }
    log('');

    // Check: depth 0 = parent, depth 1 = target iframe, depth 2 = /chat
    const parentFrame = frames.find((f) => f.depth === 0);
    const targetFrame = frames.find((f) => f.depth === 1 && f.url.includes('neoma.bimehma.com:8443'));
    const chatFrame = frames.find((f) => f.depth === 2 && f.url.includes('/chat'));

    const parentOk = parentFrame && parentFrame.origin.includes(PARENT_HOST);
    const targetOk = targetFrame && targetFrame.origin.includes('neoma.bimehma.com') && !targetFrame.url.startsWith('chrome-error');
    const chatOk = chatFrame && chatFrame.origin.includes('neoma.bimehma.com') && !chatFrame.url.startsWith('chrome-error');

    log('      Parent (' + PARENT_HOST + '):        ' + (parentOk ? 'LOADED' : 'FAILED'));
    log('      Target (neoma:8443):    ' + (targetOk ? 'LOADED' : 'BLOCKED'));
    log('      /chat iframe:           ' + (chatOk ? 'LOADED' : 'BLOCKED'));

    if (targetOk && chatOk) {
      verdict = 'PASS';
      detail = 'clmtestb.bimehma.com can frame neoma.bimehma.com:8443 and /chat loads.';
    } else if (targetOk && !chatOk) {
      verdict = 'PARTIAL';
      detail = 'neoma:8443 loads but /chat iframe is blocked (check CSP on /chat response).';
    } else {
      verdict = 'FAIL';
      detail = 'neoma.bimehma.com:8443 is blocked from clmtestb.bimehma.com.';
    }

    if (violations.length) {
      log('');
      log('      CSP violations detected by browser:');
      for (const v of violations) log('        - ' + v);
    }

    ws.close();
  } catch (e) {
    detail = 'Chrome/CDP error: ' + e.message;
  }

  // Cleanup
  try { proc.kill('SIGKILL'); } catch {}
  try { server.close(); } catch {}

  // 5. Verdict
  log('');
  log('[5/5] RESULT');
  log('');
  if (verdict === 'PASS') {
    log('  +----------------------------------+');
    log('  |  PASS  - CSP is working          |');
    log('  +----------------------------------+');
  } else if (verdict === 'PARTIAL') {
    log('  +----------------------------------+');
    log('  |  PARTIAL - see details           |');
    log('  +----------------------------------+');
  } else {
    log('  +----------------------------------+');
    log('  |  FAIL  - CSP is NOT working       |');
    log('  +----------------------------------+');
  }
  log('');
  log('  ' + detail);
  log('');

  process.exit(verdict === 'PASS' ? 0 : 1);
}

main().catch((e) => {
  console.error('FATAL:', e.message);
  process.exit(1);
});