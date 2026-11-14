// Info tooltips (.nc-info, artifactsd-5ji) open to the right of their icon by
// default. An icon in the right half of the window opens its box to the left
// instead, so it never runs off-screen. Delegated on document so it keeps
// working after htmx swaps panels in.
(function () {
  function place(event) {
    var icon = event.target.closest && event.target.closest(".nc-info");
    if (!icon) {
      return;
    }
    var rect = icon.getBoundingClientRect();
    icon.classList.toggle("nc-info-left", rect.left > window.innerWidth / 2);
  }
  document.addEventListener("mouseover", place);
  document.addEventListener("focusin", place);
})();
