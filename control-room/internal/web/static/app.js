"use strict";
// Control Room review page script.
//
// This is the only script permitted by the page CSP (script-src 'self'). It
// exists for one security reason: to send decisions as a same-origin fetch that
// always carries an Origin header and a CSRF token. Plain HTML form POSTs can
// omit the Origin header in some browsers, which would weaken the server's
// same-origin mutation check; a fetch() from this origin always sets Origin.
//
// It contains no remote calls, no eval, and no dynamic code loading.
(function () {
  var form = document.getElementById("decide-form");
  if (!form) return;
  var statusEl = document.getElementById("status");

  function selectedActionIds() {
    var boxes = form.querySelectorAll(".action-checkbox");
    var ids = [];
    for (var i = 0; i < boxes.length; i++) {
      if (boxes[i].checked) ids.push(boxes[i].value);
    }
    return ids;
  }

  function setStatus(msg) {
    if (statusEl) statusEl.textContent = msg;
  }

  function submit(decision) {
    var body = {
      csrf: form.elements["csrf"].value,
      session: form.elements["session"].value,
      revision: parseInt(form.elements["revision"].value, 10),
      decision: decision,
      reason: form.elements["reason"].value,
      selected_action_ids: selectedActionIds()
    };
    setStatus("Submitting…");
    fetch("/api/decide", {
      method: "POST",
      headers: { "Content-Type": "application/json" },
      // Same-origin only; the browser sets Origin automatically.
      credentials: "same-origin",
      body: JSON.stringify(body)
    })
      .then(function (resp) {
        return resp.json().then(function (data) {
          return { ok: resp.ok, data: data };
        });
      })
      .then(function (r) {
        if (r.ok && r.data.ok) {
          setStatus("Decision recorded: " + r.data.decision + ". Reloading…");
          window.location.reload();
        } else {
          setStatus("Rejected: " + (r.data.error || "unknown error"));
        }
      })
      .catch(function (e) {
        setStatus("Network error: " + e);
      });
  }

  var buttons = form.querySelectorAll("button[data-decision]");
  for (var i = 0; i < buttons.length; i++) {
    buttons[i].addEventListener("click", function (ev) {
      submit(ev.currentTarget.getAttribute("data-decision"));
    });
  }
})();
