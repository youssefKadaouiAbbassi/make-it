// A page behind a login, for the shots cases: POST /login sets a cookie, /private needs it.
// Usage: node server.mjs LOGFILE   (prints its port; one line per /private request in LOGFILE)
import http from 'node:http';
import fs from 'node:fs';

const log = process.argv[2];
const html = body => '<!doctype html><html><head><title>shots login test</title></head><body>' + body + '</body></html>';
const server = http.createServer((req, res) => {
  const signedIn = /(^|;\s*)session=dev(;|$)/.test(req.headers.cookie || '');
  if (req.url.startsWith('/login') && req.method === 'POST') {
    res.writeHead(303, {'Set-Cookie': 'session=dev; Path=/', Location: '/private'});
    return res.end();
  }
  if (req.url.startsWith('/login')) {
    res.writeHead(200, {'Content-Type': 'text/html'});
    return res.end(html('<form method="post" action="/login"><input name="user"><button>Sign in</button></form>'));
  }
  if (req.url.startsWith('/private')) {
    fs.appendFileSync(log, (signedIn ? 'private ok' : 'private refused') + '\n');
    if (!signedIn) {
      res.writeHead(303, {Location: '/login'});
      return res.end();
    }
    res.writeHead(200, {'Content-Type': 'text/html'});
    return res.end(html('<h1>Welcome dev</h1>'));
  }
  res.writeHead(404);
  res.end();
});
server.listen(0, '127.0.0.1', () => console.log(server.address().port));
