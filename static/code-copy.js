// Copy-to-clipboard for the code blocks rendered by the site's markdown
// pipeline (see handlers/markdown.go). The copy buttons are injected
// server-side; this script only wires up the click action. Event delegation
// on document means buttons keep working in htmx-swapped content (admin
// previews, boosted navigations) without any re-binding.
(function () {
  "use strict";

  function fallbackCopy(text) {
    var ta = document.createElement("textarea");
    ta.value = text;
    ta.setAttribute("readonly", "");
    ta.style.position = "fixed";
    ta.style.opacity = "0";
    document.body.appendChild(ta);
    ta.select();
    try {
      document.execCommand("copy");
    } catch (e) {
      /* ignore */
    }
    document.body.removeChild(ta);
  }

  function copyToClipboard(text) {
    if (navigator.clipboard && window.isSecureContext) {
      return navigator.clipboard.writeText(text).catch(function () {
        fallbackCopy(text);
      });
    }
    fallbackCopy(text);
    return Promise.resolve();
  }

  document.addEventListener("click", function (e) {
    var btn = e.target.closest(".code-copy-btn");
    if (!btn) return;
    var block = btn.closest(".code-block");
    if (!block) return;
    var code = block.querySelector("pre code");
    if (!code) return;

    var label = btn.textContent;
    copyToClipboard(code.innerText).then(function () {
      btn.textContent = "Copied!";
      btn.classList.add("copied");
      window.setTimeout(function () {
        btn.textContent = label;
        btn.classList.remove("copied");
      }, 1500);
    });
  });
})();
