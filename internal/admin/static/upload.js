// File upload with progress (design/09 §4): begin a session via the
// admin's begin-upload endpoint, then PUT the bytes directly to the returned
// capability URL (no further admin auth needed for that PUT). Delegated on
// document so it keeps working after htmx swaps #tree-panel back in.
document.addEventListener("submit", function (event) {
  var form = event.target.closest(".nc-large-upload");
  if (!form) {
    return;
  }
  event.preventDefault();

  var fileInput = form.querySelector('input[type="file"]');
  var file = fileInput && fileInput.files[0];
  var status = form.querySelector(".nc-upload-status");
  var progress = form.querySelector(".nc-upload-progress");
  if (!file) {
    status.textContent = "no file selected";
    return;
  }

  var adminPath = form.dataset.adminPath;
  var collection = form.dataset.collection;
  var dir = form.dataset.dir;
  // Optional destination subfolder (artifactsd-ok0); intermediate folders are
  // created server-side. Only the subdir may add path segments; the filename is
  // used as-is.
  var subdirInput = form.querySelector('input[name="subdir"]');
  var subdir = subdirInput ? subdirInput.value.replace(/^\/+|\/+$/g, "") : "";
  var prefix = dir;
  if (subdir) {
    prefix = prefix ? prefix + "/" + subdir : subdir;
  }
  var targetPath = prefix ? prefix + "/" + file.name : file.name;
  var csrfToken = document.querySelector('meta[name="csrf-token"]').content;

  status.textContent = "starting...";
  fetch(adminPath + "/collections/" + encodeURIComponent(collection) + "/upload/begin", {
    method: "POST",
    headers: { "Content-Type": "application/json", "X-CSRF-Token": csrfToken },
    body: JSON.stringify({ path: targetPath, content_type: file.type }),
  })
    .then(function (res) {
      if (!res.ok) {
        return res.json().then(function (body) {
          throw new Error(body.error || "begin-upload failed");
        });
      }
      return res.json();
    })
    .then(function (session) {
      progress.hidden = false;
      status.textContent = "uploading...";
      return putWithProgress(session.upload_url, file, progress);
    })
    .then(function () {
      status.textContent = "done";
      progress.hidden = true;
      var treePanel = document.getElementById("tree-panel");
      window.htmx.ajax("GET", window.location.pathname + window.location.search, {
        target: treePanel,
        swap: "outerHTML",
      });
    })
    .catch(function (err) {
      status.textContent = "error: " + err.message;
      progress.hidden = true;
    });
});

// putWithProgress streams file to url via XHR PUT (fetch has no portable
// upload-progress event), resolving once the server returns 2xx.
function putWithProgress(url, file, progress) {
  return new Promise(function (resolve, reject) {
    var xhr = new XMLHttpRequest();
    xhr.open("PUT", url);
    if (file.type) {
      xhr.setRequestHeader("Content-Type", file.type);
    }
    xhr.upload.onprogress = function (e) {
      if (e.lengthComputable) {
        progress.value = Math.round((e.loaded / e.total) * 100);
      }
    };
    xhr.onload = function () {
      if (xhr.status >= 200 && xhr.status < 300) {
        resolve();
      } else {
        reject(new Error("upload failed (" + xhr.status + ")"));
      }
    };
    xhr.onerror = function () {
      reject(new Error("upload failed"));
    };
    xhr.send(file);
  });
}
