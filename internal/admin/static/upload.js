// Large-file upload with progress (design/09 §4): begin a session via the
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

// Small-file upload feedback. The small form is a plain htmx multipart POST;
// without this it showed nothing while the bytes were in flight, and htmx
// silently drops 4xx/5xx responses (e.g. a reverse proxy's 413), so a slow or
// rejected upload looked like a dead button. A successful response swaps the
// whole #tree-panel, which removes the progress bar along with the form.
function smallUploadParts(event) {
  var form = event.target.closest && event.target.closest(".nc-small-upload");
  if (!form) {
    return null;
  }
  return {
    status: form.querySelector(".nc-upload-status"),
    progress: form.querySelector(".nc-upload-progress"),
  };
}

document.addEventListener("htmx:beforeRequest", function (event) {
  var parts = smallUploadParts(event);
  if (!parts) {
    return;
  }
  parts.progress.value = 0;
  parts.progress.hidden = false;
  parts.status.textContent = "uploading...";
});

document.addEventListener("htmx:xhr:progress", function (event) {
  var parts = smallUploadParts(event);
  if (!parts || !event.detail.lengthComputable) {
    return;
  }
  parts.progress.value = Math.round((event.detail.loaded / event.detail.total) * 100);
  if (event.detail.loaded === event.detail.total) {
    parts.status.textContent = "processing...";
  }
});

document.addEventListener("htmx:responseError", function (event) {
  var parts = smallUploadParts(event);
  if (!parts) {
    return;
  }
  var xhr = event.detail.xhr;
  // Our handlers answer errors in plain text; a proxy's HTML error page is
  // not worth echoing, so only the status is shown for those.
  var message = (xhr.responseText || "").trim().split("\n")[0];
  if (message.charAt(0) === "<") {
    message = "";
  }
  if (xhr.status === 413) {
    message = "file too large for the server or a proxy in front of it; try Upload large";
  }
  parts.progress.hidden = true;
  parts.status.textContent = "error (" + xhr.status + ")" + (message ? ": " + message : "");
});

document.addEventListener("htmx:sendError", function (event) {
  var parts = smallUploadParts(event);
  if (!parts) {
    return;
  }
  parts.progress.hidden = true;
  parts.status.textContent = "error: upload failed (network error or connection reset)";
});
