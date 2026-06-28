// Metadata editor enhancements (artifactsd-a0f). Each expiry mechanism has an
// enable checkbox (.nc-toggle) that gates its value field; the server only
// honors a mechanism whose checkbox is checked, so this is purely UX. Delegated
// on document so it keeps working after htmx swaps #meta-panel back in.
//
// - Toggling a checkbox enables/disables its paired field (the field's id is
//   named by the checkbox's aria-controls), and focuses it when enabling.
// - Typing into a field auto-checks its checkbox, so an edit is never silently
//   dropped because the box was left unchecked.
document.addEventListener("change", function (event) {
  var cb = event.target;
  if (!cb.classList || !cb.classList.contains("nc-toggle")) {
    return;
  }
  var field = document.getElementById(cb.getAttribute("aria-controls"));
  if (!field) {
    return;
  }
  field.disabled = !cb.checked;
  if (cb.checked) {
    field.focus();
  }
});

document.addEventListener("input", function (event) {
  var field = event.target;
  if (!field.classList || !field.classList.contains("nc-toggle-field")) {
    return;
  }
  var cb = document.querySelector('.nc-toggle[aria-controls="' + field.id + '"]');
  if (cb && field.value !== "") {
    cb.checked = true;
  }
});
