// Package uploadrequest serves the public upload-request landing page and
// accepts file uploads from unauthenticated third parties (design/15).
package uploadrequest

import (
	"bytes"
	"context"
	"crypto/rand"
	"embed"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"html/template"
	"io"
	"mime"
	"net/http"
	"path"
	"strings"
	"time"

	"github.com/jdelic/artifactsd/internal/audit"
	"github.com/jdelic/artifactsd/internal/auth"
	"github.com/jdelic/artifactsd/internal/blob"
	"github.com/jdelic/artifactsd/internal/clientip"
	"github.com/jdelic/artifactsd/internal/config"
	"github.com/jdelic/artifactsd/internal/meta"
	"github.com/jdelic/artifactsd/internal/paths"
	"github.com/jdelic/artifactsd/internal/store"
)

//go:embed templates/*.html
var templateFS embed.FS

var uploadTemplate = template.Must(template.New("upload").ParseFS(templateFS, "templates/upload.html"))

// maxBodyFiles is the upper bound on files per multipart request.
const maxBodyFiles = 100

// Deps are the handler's dependencies.
type Deps struct {
	Store   *store.Store
	Blob    blob.Store
	Config  *config.Config
	Audit   *audit.Recorder
	Logger  interface{ Error(string, ...any) }
	BaseURL string
	// Proxies resolves the client address from trusted-proxy headers; nil
	// ignores forwarded headers.
	Proxies *clientip.Resolver
}

// Handler serves /r/{request_id}.
type Handler struct {
	store   *store.Store
	blob    blob.Store
	cfg     *config.Config
	audit   *audit.Recorder
	log     interface{ Error(string, ...any) }
	baseURL string
	proxies *clientip.Resolver
}

// New builds the public upload handler.
func New(d Deps) *Handler { //nolint:gocritic // Deps is built once at construction
	return &Handler{
		store: d.Store, blob: d.Blob, cfg: d.Config, audit: d.Audit,
		log: d.Logger, baseURL: d.BaseURL, proxies: d.Proxies,
	}
}

// Routes returns the mux for /r/ routes, intended to be mounted at the root.
func (h *Handler) Routes() http.Handler {
	mux := http.NewServeMux()
	mux.Handle("GET /r/{request_id}", http.HandlerFunc(h.serveForm))
	mux.Handle("POST /r/{request_id}", http.HandlerFunc(h.handleUpload))
	mux.Handle("GET /r/{request_id}/status", http.HandlerFunc(h.serveStatus))
	return mux
}

// templateData is the data passed to upload.html.
type templateData struct {
	// Nonce authorizes the page's inline <script> and <style> under its CSP.
	Nonce            string
	Label            string
	Description      string
	MaxFileSize      int64
	MaxFileSizeHuman string
}

// serveForm renders the upload landing page (GET /r/{request_id}).
func (h *Handler) serveForm(w http.ResponseWriter, r *http.Request) {
	ur, ok := h.lookupActive(w, r)
	if !ok {
		return
	}
	// API clients get JSON status instead of HTML.
	if acceptsJSON(r) {
		writeJSON(w, http.StatusOK, h.toStatusView(ur))
		return
	}

	nonce, err := newNonce()
	if err != nil {
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	data := templateData{
		Nonce:       nonce,
		Label:       strFromPtr(ur.Label),
		Description: strFromPtr(ur.Description),
	}
	if ur.MaxFileSize != nil {
		data.MaxFileSize = *ur.MaxFileSize
		data.MaxFileSizeHuman = humanBytes(*ur.MaxFileSize)
	}

	var buf bytes.Buffer
	if err := uploadTemplate.ExecuteTemplate(&buf, "upload.html", data); err != nil {
		http.Error(w, "template error", http.StatusInternalServerError)
		return
	}
	hdr := w.Header()
	hdr.Set("Content-Type", "text/html; charset=utf-8")
	// The page runs on the main origin: allow only its own nonce'd script and
	// style, XHR back to this origin, and nothing else (artifactsd-m9e.8).
	hdr.Set("Content-Security-Policy", "default-src 'none'; script-src 'nonce-"+nonce+
		"'; style-src 'nonce-"+nonce+"'; connect-src 'self'; form-action 'self'; base-uri 'none'; frame-ancestors 'none'")
	hdr.Set("X-Content-Type-Options", "nosniff")
	// The URL is a capability; never leak it in a Referer.
	hdr.Set("Referrer-Policy", "no-referrer")
	_, _ = w.Write(buf.Bytes())
}

// newNonce returns a fresh 128-bit CSP nonce (URL-safe base64, which the CSP
// nonce grammar allows and html/template leaves unescaped).
func newNonce() (string, error) {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(b[:]), nil
}

// serveStatus returns the JSON status of an upload request (GET /r/{request_id}/status).
func (h *Handler) serveStatus(w http.ResponseWriter, r *http.Request) {
	ur, err := h.store.GetUploadRequest(r.Context(), r.PathValue("request_id"))
	if errors.Is(err, store.ErrNotFound) {
		writeJSONError(w, http.StatusNotFound, "upload request does not exist")
		return
	}
	if err != nil {
		writeJSONError(w, http.StatusInternalServerError, "internal error")
		return
	}
	writeJSON(w, http.StatusOK, h.toStatusView(ur))
}

// handleUpload accepts one or more files from a multipart form or a raw body
// (POST /r/{request_id}). No auth is checked — possession of the unguessable
// request_id is the capability (design/15 §4, design/14 §5).
func (h *Handler) handleUpload(w http.ResponseWriter, r *http.Request) {
	ur, ok := h.lookupActive(w, r)
	if !ok {
		return
	}

	// Determine effective per-file size limit.
	maxPerFile := h.cfg.Limits.MaxUploadSize
	if ur.MaxFileSize != nil && *ur.MaxFileSize < maxPerFile {
		maxPerFile = *ur.MaxFileSize
	}

	// Parse file metadata applied to each uploaded file.
	fileMeta, err := meta.FromJSON(ur.FileMetadata)
	if err != nil {
		writeJSONError(w, http.StatusInternalServerError, "invalid file_metadata in upload request")
		return
	}
	// Links created before visibility defaulted to private may store none;
	// their files are private too (artifactsd-m9e.24).
	applyDefaultVisibility(fileMeta)
	metaJSON, err := fileMeta.JSON()
	if err != nil {
		writeJSONError(w, http.StatusInternalServerError, "encoding file_metadata")
		return
	}

	// Mint a submission ID before touching any files.
	submissionID, err := auth.GenerateSubmissionID()
	if err != nil {
		writeJSONError(w, http.StatusInternalServerError, "generating submission id")
		return
	}

	ip := h.proxies.ClientIPString(r)

	c, err := h.store.GetCollection(r.Context(), ur.CollectionID)
	if err != nil {
		writeJSONError(w, http.StatusInternalServerError, "loading collection")
		return
	}

	ctx := r.Context()

	// Phase 1 — receive: stream every file to the blob store and validate the
	// request as a whole. Nothing is linked into the tree yet, so a rejected
	// request leaves only unreferenced blobs, which the janitor collects.
	rc := &receiver{
		h: h, ur: ur, ctx: ctx, maxPerFile: maxPerFile, submissionID: submissionID,
		collectionID: c.ID, metaJSON: metaJSON, seen: map[string]bool{},
		remaining: -1,
	}
	// Fail-fast quota only when every file is necessarily new: with
	// allow_overwrite a replacement does not count, so the commit decides.
	if ur.MaxFiles != nil && *ur.MaxFiles > 0 && !ur.AllowOverwrite {
		rc.remaining = *ur.MaxFiles - ur.FilesUploaded
	}

	if err := rc.receiveBody(w, r); err != nil {
		var he *httpError
		if errors.As(err, &he) {
			writeJSONError(w, he.status, he.msg)
			return
		}
		if h.log != nil {
			h.log.Error("upload request: receive", "error", err)
		}
		writeJSONError(w, http.StatusInternalServerError, "uploading files")
		return
	}
	if len(rc.files) == 0 {
		writeJSONError(w, http.StatusBadRequest, "no files received")
		return
	}

	if beforeCommitHook != nil {
		beforeCommitHook()
	}

	// Phase 2 — commit: one transaction re-checks the link's state and quota,
	// links every file and records the submission; any failure rolls back all.
	nodes := make([]*store.Node, len(rc.files))
	for i, f := range rc.files {
		nodes[i] = f.node
	}
	sub := &store.UploadRequestSubmission{
		UploadRequestID: ur.ID,
		SubmissionID:    submissionID,
		ClientIP:        &ip,
	}
	if err := h.store.CommitUploadRequestFiles(ctx, sub, nodes, ur.SingleUse, time.Now().UTC()); err != nil {
		switch {
		case errors.Is(err, store.ErrUploadRequestNotActive):
			writeJSONError(w, http.StatusGone, "upload link is no longer active")
		case errors.Is(err, store.ErrUploadRequestQuotaExceeded):
			writeJSONError(w, http.StatusConflict, "upload link has reached its file limit")
		case errors.Is(err, store.ErrUploadRequestFileExists):
			var fe *store.FileExistsError
			name := "the target"
			if errors.As(err, &fe) {
				name = path.Base(fe.Path)
			}
			writeJSONError(w, http.StatusConflict, "a file named "+name+" already exists")
		case errors.Is(err, store.ErrCollectionFull):
			writeJSONError(w, http.StatusConflict, "the collection has reached its file limit")
		case errors.Is(err, store.ErrKindConflict):
			writeJSONError(w, http.StatusConflict, "a file or folder in the target path conflicts with the upload")
		default:
			if h.log != nil {
				h.log.Error("upload request: commit submission", "error", err)
			}
			writeJSONError(w, http.StatusInternalServerError, "committing submission")
		}
		return
	}

	_ = h.audit.Record(ctx, &audit.Entry{
		Action: audit.ActionUploadRequestUpload,
		Actor:  audit.ActorAnonymous + ":" + ip,
		Target: c.Name + " " + ur.RequestID + " sub:" + submissionID,
	})

	type uploadedFile struct {
		Filename string  `json:"filename"`
		Path     string  `json:"path"`
		Size     int64   `json:"size"`
		URL      *string `json:"url,omitempty"`
		// Warning explains surprising serving behavior (artifactsd-7tl.9).
		Warning string `json:"warning,omitempty"`
	}
	uploaded := make([]uploadedFile, 0, len(rc.files))
	for _, f := range rc.files {
		// Return a URL only when uploader access is enabled and the file is
		// actually reachable anonymously (effective visibility, ancestors).
		var fileURL *string
		if ur.AllowUploaderAccess && h.publicURLAllowed(ctx, c, f.node.Path, fileMeta) {
			u := strings.TrimRight(h.baseURL, "/") + "/c/" + c.Name + "/" + paths.EscapeURL(f.node.Path)
			fileURL = &u
		}
		uploaded = append(uploaded, uploadedFile{
			Filename: f.filename,
			Path:     f.node.Path,
			Size:     *f.node.Size,
			URL:      fileURL,
			Warning:  h.sandboxWarning(ctx, c, f.node.Path, fileMeta),
		})
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"submission_id":   submissionID,
		"files":           uploaded,
		"uploader_access": ur.AllowUploaderAccess,
	})
}

// beforeCommitHook, when set (tests only), runs between the receive and commit
// phases of an upload.
var beforeCommitHook func()

// httpError is a receive-phase rejection with the status to answer.
type httpError struct {
	status int
	msg    string
}

func (e *httpError) Error() string { return e.msg }

// receivedFile is one file streamed to the blob store, awaiting commit.
type receivedFile struct {
	filename string
	node     *store.Node
}

// receiver runs the receive phase of one upload request.
type receiver struct {
	h            *Handler
	ur           *store.UploadRequest
	ctx          context.Context //nolint:containedctx // scoped to one request
	maxPerFile   int64
	submissionID string
	collectionID int64
	metaJSON     json.RawMessage
	// remaining is how many more files the link accepts (-1 = unlimited).
	remaining int64
	seen      map[string]bool
	files     []*receivedFile
}

// receiveBody reads a multipart form (one file per part) or a raw body (one
// file) into r.files.
func (rc *receiver) receiveBody(w http.ResponseWriter, r *http.Request) error {
	mediaType, _, _ := mime.ParseMediaType(r.Header.Get("Content-Type"))
	if mediaType != "multipart/form-data" {
		filename := filenameFromDisposition(r.Header.Get("Content-Disposition"))
		if filename == "" {
			filename = "file"
		}
		r.Body = http.MaxBytesReader(w, r.Body, rc.maxPerFile+1)
		return rc.receive(filename, r.Body)
	}

	r.Body = http.MaxBytesReader(w, r.Body, rc.maxPerFile*int64(maxBodyFiles+1))
	mr, err := r.MultipartReader()
	if err != nil {
		return &httpError{http.StatusBadRequest, "invalid multipart body"}
	}
	for {
		p, err := mr.NextPart()
		if errors.Is(err, io.EOF) {
			return nil
		}
		if err != nil {
			var mbe *http.MaxBytesError
			if errors.As(err, &mbe) {
				return &httpError{http.StatusRequestEntityTooLarge, "request body too large"}
			}
			return &httpError{http.StatusBadRequest, "reading multipart: " + err.Error()}
		}
		formName := p.FormName()
		if formName != "file" && formName != "files" && p.FileName() == "" {
			_ = p.Close()
			continue
		}
		filename := p.FileName()
		if filename == "" {
			filename = "file"
		}
		err = rc.receive(filename, p)
		_ = p.Close()
		if err != nil {
			return err
		}
	}
}

// receive validates one file against the request-wide limits, then streams it
// to the blob store.
func (rc *receiver) receive(filename string, rd io.Reader) error {
	if len(rc.files) >= maxBodyFiles {
		return &httpError{http.StatusBadRequest, fmt.Sprintf("too many files in one request (max %d)", maxBodyFiles)}
	}
	// Fail fast once the request is known to exceed the link's quota, before
	// reading any more of the body.
	if rc.remaining >= 0 && int64(len(rc.files)) >= rc.remaining {
		return &httpError{http.StatusConflict, "upload link has reached its file limit"}
	}

	// Logical path: {target_folder}[/{submission_id}]/{filename}
	var segments []string
	if rc.ur.TargetFolder != "" {
		segments = append(segments, rc.ur.TargetFolder)
	}
	if rc.ur.UseSubmissionFolders {
		segments = append(segments, rc.submissionID)
	}
	segments = append(segments, sanitizeFilename(filename))
	nodePath := strings.Join(segments, "/")
	if rc.seen[nodePath] {
		return &httpError{http.StatusBadRequest, "two files in this request have the same name: " + path.Base(nodePath)}
	}
	rc.seen[nodePath] = true

	// Advisory early refusal of an occupied path, before storing any bytes;
	// the commit transaction re-checks authoritatively (artifactsd-m9e.1).
	existing, err := rc.h.store.GetNode(rc.ctx, rc.collectionID, nodePath)
	switch {
	case errors.Is(err, store.ErrNotFound):
	case err != nil:
		return fmt.Errorf("checking %q: %w", nodePath, err)
	case !rc.ur.AllowOverwrite || existing.Kind != store.KindFile ||
		existing.UploadRequestID == nil || *existing.UploadRequestID != rc.ur.ID:
		return &httpError{http.StatusConflict, "a file named " + path.Base(nodePath) + " already exists"}
	}

	lr := &io.LimitedReader{R: rd, N: rc.maxPerFile + 1}
	ref, size, sum, err := rc.h.blob.Put(rc.ctx, lr)
	if err != nil {
		var mbe *http.MaxBytesError
		if errors.As(err, &mbe) {
			return &httpError{http.StatusRequestEntityTooLarge, "file " + filename + " exceeds the size limit"}
		}
		return fmt.Errorf("storing blob for %q: %w", filename, err)
	}
	if size > rc.maxPerFile {
		// The blob is stored but unreferenced; the janitor collects it.
		return &httpError{http.StatusRequestEntityTooLarge, "file " + filename + " exceeds the size limit"}
	}

	refStr := string(ref)
	node := &store.Node{
		CollectionID: rc.collectionID, Path: nodePath,
		Name: path.Base(nodePath), Kind: store.KindFile,
		BlobID: &refStr, Size: &size, Checksum: &sum,
		Metadata: rc.metaJSON,
	}
	if ct := mime.TypeByExtension(path.Ext(filename)); ct != "" {
		node.ContentType = &ct
	}
	rc.files = append(rc.files, &receivedFile{filename: filename, node: node})
	return nil
}

// --- status view -----------------------------------------------------------

type statusView struct {
	RequestID     string  `json:"request_id"`
	Label         *string `json:"label,omitempty"`
	Status        string  `json:"status"`
	FilesUploaded int64   `json:"files_uploaded"`
}

func (h *Handler) toStatusView(ur *store.UploadRequest) statusView {
	return statusView{
		RequestID:     ur.RequestID,
		Label:         ur.Label,
		Status:        ur.Status,
		FilesUploaded: ur.FilesUploaded,
	}
}

// --- helpers ---------------------------------------------------------------

func (h *Handler) lookupActive(w http.ResponseWriter, r *http.Request) (*store.UploadRequest, bool) {
	ur, err := h.store.GetUploadRequest(r.Context(), r.PathValue("request_id"))
	if errors.Is(err, store.ErrNotFound) {
		writeJSONError(w, http.StatusNotFound, "upload link not found")
		return nil, false
	}
	if err != nil {
		writeJSONError(w, http.StatusInternalServerError, "internal error")
		return nil, false
	}
	if !ur.IsActive(time.Now().UTC()) {
		writeJSONError(w, http.StatusGone, "upload link is no longer active")
		return nil, false
	}
	return ur, true
}

// acceptsJSON returns true when the client prefers a JSON response over HTML.
func acceptsJSON(r *http.Request) bool {
	accept := r.Header.Get("Accept")
	if strings.Contains(accept, "application/json") {
		return true
	}
	ua := strings.ToLower(r.Header.Get("User-Agent"))
	for _, kw := range []string{"curl", "wget", "python", "go-http", "httpie"} {
		if strings.Contains(ua, kw) {
			return true
		}
	}
	return false
}

func strFromPtr(s *string) string {
	if s == nil {
		return ""
	}
	return *s
}

func sanitizeFilename(name string) string {
	base := path.Base(name)
	if base == "." || base == "/" {
		return "file"
	}
	if clean, err := paths.Clean(base); err == nil && clean != "" {
		return clean
	}
	return "file"
}

func filenameFromDisposition(cd string) string {
	_, params, err := mime.ParseMediaType(cd)
	if err != nil {
		return ""
	}
	return params["filename"]
}

func humanBytes(n int64) string {
	const (
		KB = 1024
		MB = KB * 1024
		GB = MB * 1024
	)
	switch {
	case n >= GB:
		return fmt.Sprintf("%.1f GB", float64(n)/GB)
	case n >= MB:
		return fmt.Sprintf("%.1f MB", float64(n)/MB)
	case n >= KB:
		return fmt.Sprintf("%.1f KB", float64(n)/KB)
	default:
		return fmt.Sprintf("%d B", n)
	}
}

// --- JSON helpers (no import from api package to avoid cycles) -------------

func writeJSON(w http.ResponseWriter, status int, v any) {
	b, _ := json.Marshal(v)
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_, _ = w.Write(b)
}

func writeJSONError(w http.ResponseWriter, status int, msg string) {
	writeJSON(w, status, map[string]any{
		"error": map[string]string{"message": msg},
	})
}
