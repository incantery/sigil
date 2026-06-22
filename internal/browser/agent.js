// Sigil browser agent. Injected into every document by the driver. Connects
// back to the driver's localhost websocket and serves DOM intents.
(function () {
  if (window.__sigilAgent) return;
  window.__sigilAgent = true;
  var url = window.__SIGIL_WS_URL__;
  if (!url) return;
  var ws = new WebSocket(url);

  function visible(el) {
    if (!el) return false;
    var r = el.getBoundingClientRect();
    if (r.width === 0 && r.height === 0) return false;
    var s = window.getComputedStyle(el);
    return s.display !== "none" && s.visibility !== "hidden" && s.opacity !== "0";
  }

  // waitVisible resolves the instant the selector is visible, using a
  // MutationObserver (no polling). Times out after `ms`.
  function waitVisible(sel, ms, done) {
    var el = document.querySelector(sel);
    if (visible(el)) { done(null); return; }
    var obs = new MutationObserver(function () {
      var e = document.querySelector(sel);
      if (visible(e)) { obs.disconnect(); clearTimeout(timer); done(null); }
    });
    obs.observe(document.documentElement, { childList: true, subtree: true, attributes: true });
    var timer = setTimeout(function () {
      obs.disconnect();
      done("timeout waiting for " + sel);
    }, ms);
  }

  function reply(id, value, error) {
    ws.send(JSON.stringify({ id: id, ok: error == null, value: value || "", error: error || "" }));
  }

  function handle(msg) {
    var id = msg.id, sel = msg.sel;
    try {
      switch (msg.op) {
        case "domText": {
          var el = document.querySelector(sel);
          reply(id, el ? (el.textContent || "") : "", el ? null : "no element matches " + sel);
          break;
        }
        case "click": {
          var c = document.querySelector(sel);
          if (!c) { reply(id, "", "no element matches " + sel); break; }
          c.click();
          reply(id, "", null);
          break;
        }
        case "fill": {
          var f = document.querySelector(sel);
          if (!f) { reply(id, "", "no element matches " + sel); break; }
          f.value = msg.text;
          f.dispatchEvent(new Event("input", { bubbles: true }));
          f.dispatchEvent(new Event("change", { bubbles: true }));
          reply(id, "", null);
          break;
        }
        case "waitVisible": {
          waitVisible(sel, msg.ms || 5000, function (err) { reply(id, "", err); });
          break;
        }
        default:
          reply(id, "", "unknown op " + msg.op);
      }
    } catch (e) {
      reply(id, "", String(e));
    }
  }

  ws.onopen = function () { ws.send(JSON.stringify({ hello: true })); };
  ws.onmessage = function (ev) { handle(JSON.parse(ev.data)); };
})();
