// A white-label build's first run: sign in to the relay the build belongs
// to, add it with one of the account's keys, and connect the agents already
// on this machine. A build whose config has no account API is left alone
// (GET /api/onboard answers hasSignIn: false), and magpie's own flow
// (a key pasted by hand) stays as it was.
//
// Drawn as a layer over the app, built here, so the page's own scripts and
// markup are untouched.
(() => {
  const zh = (navigator.language || "").toLowerCase().startsWith("zh");
  const say = (en, cn) => (zh ? cn : en);
  const brand = () => window.bootPrefs?.brand?.name || "magpie";
  const short = () => window.bootPrefs?.brand?.shortName || brand();

  const el = (tag, props = {}, kids = []) => {
    const e = document.createElement(tag);
    Object.assign(e, props);
    for (const k of kids) e.append(k);
    return e;
  };

  const CSS = `
  .wl-layer{position:fixed;inset:0;z-index:99999;display:flex;align-items:center;justify-content:center;
    background:rgba(0,0,0,.45);backdrop-filter:blur(6px);font:14px/1.5 system-ui,-apple-system,"Segoe UI",sans-serif}
  .wl-card{width:min(560px,92vw);max-height:88vh;overflow:auto;background:var(--bg,#fff);color:var(--fg,#111);
    border-radius:16px;padding:28px 28px 22px;box-shadow:0 24px 80px rgba(0,0,0,.35)}
  .wl-card h1{margin:0 0 4px;font-size:22px}
  .wl-card p.sub{margin:0 0 18px;opacity:.7}
  .wl-steps{display:flex;gap:6px;margin:0 0 18px}
  .wl-steps i{height:4px;border-radius:2px;background:currentColor;opacity:.15;flex:1}
  .wl-steps i.on{opacity:.9}
  .wl-field{display:flex;flex-direction:column;gap:6px;margin:0 0 12px}
  .wl-field label{font-size:12px;opacity:.7}
  .wl-field input{padding:10px 12px;border-radius:8px;border:1px solid rgba(127,127,127,.4);
    background:transparent;color:inherit;font:inherit}
  .wl-row{display:flex;gap:10px;justify-content:flex-end;margin-top:18px}
  .wl-btn{padding:10px 18px;border-radius:9px;border:1px solid rgba(127,127,127,.35);background:transparent;
    color:inherit;font:inherit;cursor:pointer}
  .wl-btn.primary{background:var(--accent,#2563eb);border-color:transparent;color:#fff}
  .wl-btn:disabled{opacity:.5;cursor:default}
  .wl-err{color:#dc2626;font-size:13px;min-height:18px;margin:6px 0 0}
  .wl-agents{display:flex;flex-direction:column;gap:6px;margin:8px 0 0;max-height:240px;overflow:auto}
  .wl-agent{display:flex;align-items:center;gap:10px;padding:8px 10px;border-radius:8px;
    border:1px solid rgba(127,127,127,.25)}
  .wl-agent span.what{flex:1}
  .wl-agent small{opacity:.6}
  .wl-note{font-size:12px;opacity:.6;margin-top:14px}
  .wl-lines{display:flex;flex-direction:column;gap:4px;margin-top:14px}
  .wl-line{font-size:13px;padding:6px 10px;border-radius:6px;background:rgba(127,127,127,.1)}
  .wl-line.bad{color:#dc2626}
  `;

  async function post(path, body) {
    const res = await fetch("/api/onboard/" + path, {
      method: "POST",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify(body || {}),
    });
    const text = await res.text();
    let data = null;
    try { data = text ? JSON.parse(text) : null; } catch { /* not JSON */ }
    if (!res.ok) throw new Error(data?.error || data?.message || `${res.status} ${res.statusText}`);
    return data;
  }

  async function get() {
    const res = await fetch("/api/onboard");
    if (!res.ok) return null;
    return res.json();
  }

  function draw(status) {
    const layer = el("div", { className: "wl-layer" });
    const card = el("div", { className: "wl-card" });
    layer.append(card);
    document.body.append(layer);
    let step = 0;
    const steps = el("div", { className: "wl-steps" }, [el("i"), el("i"), el("i")]);
    const body = el("div");
    card.append(steps, body);

    const show = (n) => {
      step = n;
      [...steps.children].forEach((i, k) => i.classList.toggle("on", k <= n));
      body.replaceChildren();
      if (n === 0) stepSignIn();
      else if (n === 1) stepRelay();
      else stepAgents();
    };

    const head = (title, sub) => {
      body.append(el("h1", { textContent: title }), el("p", { className: "sub", textContent: sub }));
    };

    const form = (fields) => {
      const inputs = {};
      for (const f of fields) {
        const input = el("input", { type: f.type || "text", placeholder: f.placeholder || "", autocomplete: f.autocomplete || "off" });
        inputs[f.key] = input;
        body.append(el("div", { className: "wl-field" }, [el("label", { textContent: f.label }), input]));
      }
      return inputs;
    };

    const errLine = el("div", { className: "wl-err" });

    function stepSignIn() {
      head(say("Set up " + short(), "开始使用" + short()),
        say("Sign in once, and every coding tool is configured for you.",
          "登录一次，Codex、Claude Code 等工具自动配好" + short()));
      const inputs = form([
        { key: "email", label: say("Email", "邮箱"), type: "email", autocomplete: "username" },
        { key: "password", label: say("Password", "密码"), type: "password", autocomplete: "current-password" },
      ]);
      body.append(errLine);
      const later = el("button", { className: "wl-btn", textContent: say("Later", "稍后") });
      const go = el("button", { className: "wl-btn primary", textContent: say("Sign in", "登录") });
      body.append(el("div", { className: "wl-row" }, [later, go]));
      later.onclick = async () => { try { await post("finish"); } catch { /* leave */ } layer.remove(); };
      go.onclick = async () => {
        errLine.textContent = "";
        go.disabled = true;
        try {
          await post("signin", { email: inputs.email.value.trim(), password: inputs.password.value });
          show(1);
        } catch (e) {
          errLine.textContent = e.message;
          go.disabled = false;
        }
      };
      inputs.password.onkeydown = (e) => { if (e.key === "Enter") go.click(); };
      inputs.email.focus();
    }

    function stepRelay() {
      head(say("Adding " + brand(), "正在接入" + brand()),
        say("One of your account's keys is used, made first if you have none.",
          "将使用你账号下的 Key，没有会自动创建一个"));
      const note = el("p", { className: "sub", textContent: say("Contacting the relay…", "正在连接中转站…") });
      body.append(note, errLine);
      const next = el("button", { className: "wl-btn primary", textContent: say("Continue", "继续"), disabled: true });
      body.append(el("div", { className: "wl-row" }, [next]));
      next.onclick = () => show(2);
      // the lines, timed: which answers, and how fast
      const linesSoon = () => {
        const box = el("div", { className: "wl-lines" });
        body.append(box);
        fetch("/api/onboard/lines?probe=1")
          .then((r) => r.json())
          .then((d) => {
            for (const ln of d?.lines || []) {
              const what = ln.ok ? ln.latencyMs + " ms" : say("no answer", "超时");
              box.append(el("div", {
                className: "wl-line" + (ln.ok ? "" : " bad"),
                textContent: ln.name + "  " + what,
              }));
            }
          })
          .catch(() => {});
      };
      post("relay")
        .then((out) => {
          note.textContent = say("Done — the relay is added.", "完成，中转站已添加。");
          if (out?.group) {
            note.textContent += " " + say("Its lines fail over to each other.", "线路之间会自动切换。");
          }
          next.disabled = false;
          linesSoon();
        })
        .catch((e) => {
          note.textContent = "";
          errLine.textContent = e.message + " " + say("You can also paste a key later under Providers.", "也可以稍后在 Providers 里粘贴 Key。");
          next.disabled = false;
        });
    }

    function stepAgents() {
      head(say("Connect your tools", "接入你的工具"),
        say("Pick the ones to point at " + short() + ".", "选择要接入" + short() + "的工具"));
      const list = el("div", { className: "wl-agents" });
      const chosen = new Set();
      const agents = status.detected || [];
      if (!agents.length) {
        body.append(el("p", { className: "sub", textContent: say("No agents found on this machine yet.", "这台电脑上还没有检测到工具。") }));
      }
      for (const a of agents) {
        const box = el("input", { type: "checkbox", checked: !a.connected });
        if (a.connected) box.disabled = true;
        if (!a.connected) chosen.add(a.id);
        box.onchange = () => (box.checked ? chosen.add(a.id) : chosen.delete(a.id));
        list.append(el("div", { className: "wl-agent" }, [
          box,
          el("span", { className: "what", textContent: a.name }),
          a.connected ? el("small", { textContent: say("connected", "已接入") }) : el("small", { textContent: "" }),
        ]));
      }
      body.append(list, errLine);
      const go = el("button", { className: "wl-btn primary", textContent: say("Connect", "一键接入") });
      const skip = el("button", { className: "wl-btn", textContent: say("Finish", "完成") });
      body.append(el("div", { className: "wl-row" }, [skip, go]));
      const done = async () => { try { await post("finish"); } catch { /* leave */ } layer.remove(); };
      skip.onclick = done;
      go.onclick = async () => {
        errLine.textContent = "";
        go.disabled = true;
        try {
          const out = await post("connect", { ids: [...chosen] });
          const errs = Object.entries(out?.errors || {});
          if (errs.length) {
            errLine.textContent = errs.map(([id, msg]) => id + ": " + msg).join("; ");
            go.disabled = false;
            return;
          }
          await done();
        } catch (e) {
          errLine.textContent = e.message;
          go.disabled = false;
        }
      };
      body.append(el("p", { className: "wl-note", textContent: say("Later, more tools can be connected under Agents.", "以后可以在 Agents 页接入更多工具。") }));
    }

    show(status.signedIn ? (status.provider ? 2 : 1) : 0);
  }

  async function start() {
    if (document.getElementById("wl-onboard")) return;
    let status = null;
    try { status = await get(); } catch { return; }
    if (!status?.hasSignIn || status.done) return;
    const style = el("style", { textContent: CSS });
    document.head.append(style);
    draw(status);
  }

  if (document.readyState === "loading") document.addEventListener("DOMContentLoaded", start);
  else start();
})();
