// The page the element picker's preview scenario shows on agent-25's desktop
// (dev/pick.tsx, ?pick=1): a settings page whose Save button doesn't match the
// header's, the kind of thing you pick both of and say "make 1 match 2".
// dev/pickFixture.ts was picked from this same page in a real Chromium.
export const pickDemoUrl = 'http://localhost:3000/settings/';

export const pickDemoPage = `<!doctype html>
<html lang="en"><head><meta charset="utf-8"><title>Settings · Pawly</title><style>
  * { box-sizing: border-box; }
  body { margin: 0; font: 15px/1.5 system-ui, -apple-system, 'Segoe UI', sans-serif; color: #1f2333; background: #f4f5fa; }
  header { display: flex; align-items: center; gap: 28px; height: 64px; padding: 0 40px; background: #1e1b4b; color: #e0e7ff; }
  header .logo { font-weight: 800; font-size: 20px; letter-spacing: -0.02em; color: #fff; }
  header nav { display: flex; gap: 20px; flex: 1; font-size: 14px; }
  header nav a { color: #c7d2fe; text-decoration: none; }
  header nav a.active { color: #fff; font-weight: 600; }
  .btn-upgrade { padding: 8px 18px; border: 0; border-radius: 999px; background: linear-gradient(135deg, #8b5cf6, #6366f1); color: #fff; font-weight: 600; font-size: 14px; box-shadow: 0 4px 14px rgb(99 102 241 / 0.45); cursor: pointer; }
  main { max-width: 720px; margin: 40px auto; padding: 0 24px; }
  h1 { margin: 0 0 6px; font-size: 26px; }
  p.lead { margin: 0 0 24px; color: #5b6075; }
  .card { background: #fff; border: 1px solid #e3e5ef; border-radius: 14px; padding: 28px; }
  label { display: block; margin: 0 0 16px; font-size: 13px; font-weight: 600; color: #3b4054; }
  input, select { display: block; width: 100%; margin-top: 6px; padding: 10px 12px; border: 1px solid #d6d9e6; border-radius: 8px; font: inherit; background: #fff; }
  .row { display: flex; gap: 16px; } .row label { flex: 1; }
  .actions { display: flex; justify-content: flex-end; gap: 10px; margin-top: 8px; }
  .btn-cancel { padding: 9px 16px; border: 1px solid #d6d9e6; border-radius: 8px; background: #fff; font: inherit; font-size: 14px; }
  .btn-save { padding: 9px 16px; border: 1px solid #9aa0b4; border-radius: 3px; background: #e9eaf0; color: #3b4054; font: inherit; font-size: 14px; }
</style></head><body>
<header>
  <span class="logo">Pawly</span>
  <nav><a href="#">Reminders</a><a href="#">Pets</a><a class="active" href="#">Settings</a></nav>
  <button class="btn-upgrade" type="button">Upgrade to Pro</button>
</header>
<main>
  <h1>Settings</h1>
  <p class="lead">How Pawly reminds you about walks, meals and the vet.</p>
  <section class="card">
    <div class="row">
      <label>Name<input value="Ana Souza"></label>
      <label>Email<input value="ana@pawly.app"></label>
    </div>
    <label>Remind me<select><option>15 minutes before</option><option>An hour before</option></select></label>
    <label>Time zone<select><option>America/Sao_Paulo</option></select></label>
    <div class="actions">
      <button class="btn-cancel" type="button">Cancel</button>
      <button id="save" class="btn-save" type="submit">Save changes</button>
    </div>
  </section>
</main>
</body></html>`;
