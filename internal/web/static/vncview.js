// Settings' Browser tab: the login browser's screen, live, through noVNC's
// client (served at /novnc/), and the platforms' tab buttons, which act
// without reloading the page (and so without dropping the screen).
import RFB from '/novnc/core/rfb.js';

const box = document.querySelector('[data-vnc]');
const msg = box && box.querySelector('.vnc-msg');
let connected = false;

// say shows what the screen can't: connecting, or the browser closed.
function say() {
  if (!box) return;
  const text = !connected ? 'Connecting to the screen…'
    : box.hasAttribute('data-running') ? ''
    : 'The browser is closed. Pick a platform above to open it.';
  msg.textContent = text;
  msg.hidden = text === '';
}

function connect() {
  const url = (location.protocol === 'https:' ? 'wss://' : 'ws://') + location.host + box.dataset.vnc;
  const rfb = new RFB(box.querySelector('.vnc-screen'), url, { shared: true });
  rfb.scaleViewport = true; // the whole screen, fitted to the page
  rfb.background = 'transparent';
  rfb.addEventListener('connect', () => { connected = true; say(); });
  rfb.addEventListener('disconnect', () => {
    connected = false;
    say();
    setTimeout(connect, 2000);
  });
}

if (box) {
  say();
  connect();
}

const errLine = document.querySelector('.bwr-err');
function showError(text) {
  if (!errLine) return;
  errLine.textContent = text;
  errLine.hidden = text === '';
}

// The buttons post as forms without a script; with it, they stay on the page.
for (const form of document.querySelectorAll('form[data-browser]')) {
  form.addEventListener('submit', async (e) => {
    e.preventDefault();
    const button = e.submitter || form.querySelector('button');
    button.disabled = true;
    try {
      const res = await fetch(form.action, { method: 'POST', body: new FormData(form), headers: { Accept: 'application/json' } });
      const reply = await res.json();
      if (box) box.toggleAttribute('data-running', reply.running);
      say();
      showError(reply.error || '');
    } catch {
      form.submit();
    } finally {
      button.disabled = false;
    }
  });
}
