// Persistent HTTPS parent server for manual browser verification.
// Serves https://clmtestb.bimehma.com/ which embeds neoma.bimehma.com:8443 in an iframe.
// Run: node verify-csp-server.js
// Keep it running while you test in the browser. Ctrl+C to stop.
const https = require('https');
const fs = require('fs');
const path = require('path');

const CERTDIR = 'C:\\Users\\Lenovo\\AppData\\Local\\Temp\\opencode\\csp-certs';
const TARGET = 'https://neoma.bimehma.com:8443/';
const PARENT_HOST = 'clmtestb.bimehma.com';

const parentHtml = `<!doctype html>
<html lang="en">
<head>
<meta charset="utf-8">
<title>CSP Verify - clmtestb.bimehma.com embedding neoma:8443</title>
<style>
  body { margin: 0; font: 14px/1.4 -apple-system, system-ui, sans-serif; padding: 8px; background: #1a1a2e; color: #e0e0e0; }
  h1 { font-size: 15px; margin: 0 0 6px; color: #8be9fd; }
  .bar { background: #16213e; padding: 10px 12px; border-radius: 6px; margin-bottom: 8px; }
  .bar b { color: #50fa7b; }
  .bar code { color: #ffb86c; background: #0d1117; padding: 1px 5px; border-radius: 3px; }
  iframe { width: 100%; height: calc(100vh - 90px); border: 2px solid #444; border-radius: 6px; background: #fff; }
</style>
</head>
<body>
<div class="bar">
  <h1>CSP frame-ancestors verification</h1>
  Parent origin: <b>${PARENT_HOST}</b> &rarr; embedding <code>${TARGET}</code><br>
  If you can see the chat app below, CSP is working. If you see a blank/error frame, it is blocked.
</div>
<iframe id="f" src="${TARGET}"></iframe>
</body>
</html>`;

const options = {
  key: fs.readFileSync(path.join(CERTDIR, 'key.pem')),
  cert: fs.readFileSync(path.join(CERTDIR, 'cert.pem')),
};

const server = https.createServer(options, (req, res) => {
  res.writeHead(200, { 'Content-Type': 'text/html; charset=utf-8' });
  res.end(parentHtml);
});

server.on('error', (e) => { console.error('Server error:', e.code); process.exit(1); });

server.listen(443, '127.0.0.2', () => {
  console.log('');
  console.log('Parent HTTPS server running on https://127.0.0.1:443/');
  console.log('Serving as: https://' + PARENT_HOST + '/');
  console.log('Embedding: ' + TARGET);
  console.log('');
  console.log('Now open Chrome with the host override and visit:');
  console.log('  https://clmtestb.bimehma.com/');
  console.log('');
  console.log('Chrome launch command (already mapped):');
  console.log('  "C:\\Program Files\\Google\\Chrome\\Application\\chrome.exe" \\');
  console.log('    --user-data-dir=C:\\csp-test-profile \\');
  console.log('    --ignore-certificate-errors \\');
  console.log('    --host-resolver-rules="MAP clmtestb.bimehma.com 127.0.0.1, MAP neoma.bimehma.com 127.0.0.1" \\');
  console.log('    https://clmtestb.bimehma.com/');
  console.log('');
  console.log('Press Ctrl+C to stop the server.');
});