"use strict";
// Sigil dev client agent. Owns the cross-reload reactive registry and the
// in-place hot-module-replacement lifecycle. Loaded before the initial bundle.
(function () {
  window.__sigilDev = {
    counter: 0,
    hydration: new Map(),
    cells: new Map(),
    disposers: [],
    generation: 0,
  };

  function runBundle(src) {
    // Fresh function scope each time: the bundle redeclares its own const
    // intrinsics without colliding with a prior eval.
    new Function(src)();
  }

  function teardown() {
    var dev = window.__sigilDev;
    // Snapshot live cell values by their creation-order index.
    var snap = new Map();
    dev.cells.forEach(function (cell, i) { snap.set(i, cell.v); });
    // Dispose global listeners (popstate, etc.) and invalidate in-flight fetches.
    dev.disposers.forEach(function (d) { try { d(); } catch (e) {} });
    dev.disposers = [];
    dev.generation++;
    // Empty the mount root.
    var app = document.querySelector("#app");
    if (app) app.replaceChildren();
    dev.cells = new Map();
    dev.counter = 0;
    return snap;
  }

  function hotSwap(src) {
    var snap = teardown();
    window.__sigilDev.hydration = snap;
    runBundle(src);
    window.__sigilDev.hydration = new Map();
    hideOverlay();
  }

  // --- build-error overlay -------------------------------------------------
  function overlay() {
    var el = document.getElementById("__sigil_overlay");
    if (!el) {
      el = document.createElement("pre");
      el.id = "__sigil_overlay";
      el.style.cssText =
        "position:fixed;inset:0;margin:0;padding:24px;z-index:2147483647;" +
        "background:rgba(20,20,20,.95);color:#f88;font:13px/1.5 monospace;" +
        "white-space:pre-wrap;overflow:auto;";
      el.addEventListener("click", hideOverlay);
      document.body.appendChild(el);
    }
    return el;
  }
  function showOverlay(msg) { overlay().textContent = "build error\n\n" + msg + "\n\n(click to dismiss)"; }
  function hideOverlay() {
    var el = document.getElementById("__sigil_overlay");
    if (el) el.remove();
  }

  // --- live connection -----------------------------------------------------
  var es = new EventSource("/__sigil/events");
  es.onmessage = function (e) {
    var msg = JSON.parse(e.data);
    if (msg.type === "reload") hotSwap(msg.bundle);
    else if (msg.type === "error") showOverlay(msg.message);
  };
})();
