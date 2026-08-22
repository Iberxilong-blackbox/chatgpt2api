// Turnstile-only page helper. It does not modify browser fingerprints.
(function () {
  "use strict";

  var maxChecks = 120;
  var checkCount = 0;
  var timer = null;

  function tokenReady() {
    try {
      var input = document.querySelector('input[name="cf-turnstile-response"]');
      if (input && String(input.value || "").trim()) return true;
      return !!(
        window.turnstile &&
        typeof window.turnstile.getResponse === "function" &&
        String(window.turnstile.getResponse() || "").trim()
      );
    } catch (e) {
      return false;
    }
  }

  function clickCheckbox(root) {
    try {
      var checkbox = root.querySelector(
        'input[type="checkbox"], .mark, #cf-chl-widget-nomu1_resp'
      );
      if (checkbox && !checkbox.checked && typeof checkbox.click === "function") {
        checkbox.click();
        return true;
      }
    } catch (e) {}
    return false;
  }

  function scanTurnstileFrames() {
    var clicked = clickCheckbox(document);
    var frames = document.querySelectorAll(
      'iframe[src*="challenges.cloudflare.com"], iframe[src*="turnstile"]'
    );

    for (var i = 0; i < frames.length; i += 1) {
      var frame = frames[i];
      try {
        var body = frame.contentDocument || frame.contentWindow.document;
        clicked = clickCheckbox(body) || clicked;
      } catch (e) {
        try {
          frame.contentWindow.postMessage({ type: "turnstile-auto-click" }, "*");
        } catch (e2) {}
      }
    }
    return clicked;
  }

  function start() {
    if (timer) return;
    timer = setInterval(function () {
      checkCount += 1;
      if (checkCount > maxChecks || tokenReady()) {
        clearInterval(timer);
        timer = null;
        return;
      }
      scanTurnstileFrames();
    }, 500);
  }

  if (document.readyState === "loading") {
    document.addEventListener("DOMContentLoaded", start, { once: true });
  } else {
    start();
  }
})();
