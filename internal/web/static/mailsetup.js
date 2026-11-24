// Settings › HuntMail, while a setup runs: asks every second or so how far
// it's got, and reloads the page when a step finishes or the run ends
// (requests fail for a moment while HuntDeck restarts to use the new key).
const run = document.querySelector('[data-setup-running]');
let seen = run ? run.querySelectorAll('.msetup-steps li').length : 0;

async function check() {
  try {
    const res = await fetch('/settings/huntmail/status', { cache: 'no-store', headers: { Accept: 'application/json' } });
    if (res.ok) {
      const st = await res.json();
      if (st.saved) {
        location.replace('/settings?tab=email&msg=huntmail-set-up#acct-huntmail');
        return;
      }
      if (!st.running || st.steps !== seen) {
        location.replace('/settings/huntmail');
        return;
      }
    }
  } catch {
    // restarting: try again
  }
  setTimeout(check, 1200);
}

if (run) setTimeout(check, 800);
