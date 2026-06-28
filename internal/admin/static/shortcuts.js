// Admin keyboard shortcuts (design/09; artifactsd-9sy). The footer advertises
// Alt+<key> combos rather than DOS-style function keys: browsers reserve
// F5/F10/F11/F12 (reload, menu, fullscreen, devtools) and will not let a page
// intercept them, so the old F-key labels were decorative. Alt+<letter>
// matches the underlined mnemonics already used in the top bar, works even
// while a form field is focused (so "Save" is reachable while editing), and
// avoids the reserved Ctrl+N/W/T combos.
//
// Each shortcut targets the element carrying a matching data-shortcut="<letter>"
// attribute. data-shortcut-action selects what happens (default "click"):
//   - "click"  click the element (buttons/links; htmx handles form submits)
//   - "focus"  move focus to the element (and select its text, for inputs)
// Delegated on document so it keeps working after htmx swaps panels back in.
document.addEventListener("keydown", function (event) {
  // Only plain Alt+<letter>: ignore Ctrl/Meta-modified combos so we never
  // shadow a real browser shortcut.
  if (!event.altKey || event.ctrlKey || event.metaKey) {
    return;
  }
  var key = (event.key || "").toLowerCase();
  if (!/^[a-z0-9]$/.test(key)) {
    return;
  }
  var target = document.querySelector('[data-shortcut="' + key + '"]');
  if (!target) {
    return;
  }
  event.preventDefault();
  if (target.getAttribute("data-shortcut-action") === "focus") {
    target.focus();
    if (typeof target.select === "function") {
      target.select();
    }
    return;
  }
  target.click();
});
