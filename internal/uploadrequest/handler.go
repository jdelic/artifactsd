// Package uploadrequest serves the public upload-request landing page and
// accepts file uploads from unauthenticated third parties (design/15).
package uploadrequest

import (
	"bytes"
	"context"
	"embed"
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
}

// Handler serves /r/{request_id}.
type Handler struct {
	store   *store.Store
	blob    blob.Store
	cfg     *config.Config
	audit   *audit.Recorder
	log     interface{ Error(string, ...any) }
	baseURL string
}

// New builds the public upload handler.
func New(d Deps) *Handler {
	return &Handler{
		store: d.Store, blob: d.Blob, cfg: d.Config, audit: d.Audit,
		log: d.Logger, baseURL: d.BaseURL,
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

	data := templateData{
		Label:       strVal(ur.Label),
		Description: strVal(ur.Description),
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
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	_, _ = w.Write(buf.Bytes())
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

	ip := clientIP(r)

	ct := r.Header.Get("Content-Type")
	mediaType, _, _ := mime.ParseMediaType(ct)

	type uploadedFile struct {
		Filename string  `json:"filename"`
		Path     string  `json:"path"`
		Size     int64   `json:"size"`
		URL      *string `json:"url,omitempty"`
	}
	var uploaded []uploadedFile
	var totalBytes int64

	c, err := h.store.GetCollection(r.Context(), ur.CollectionID)
	if err != nil {
		writeJSONError(w, http.StatusInternalServerError, "loading collection")
		return
	}

	ctx := r.Context()

	processFile := func(filename string, reader io.Reader) error {
		// Enforce per-file size limit.
		lr := &io.LimitedReader{R: reader, N: maxPerFile + 1}
		ref, size, sum, err := h.blob.Put(ctx, lr)
		if err != nil {
			return fmt.Errorf("storing blob for %q: %w", filename, err)
		}
		if size > maxPerFile {
			// Blob already stored but over-size; leave for GC — same pattern as API upload.
			return &oversizeError{filename: filename}
		}

		// Determine logical path: {target_folder}[/{submission_id}]/{filename}
		var segments []string
		if ur.TargetFolder != "" {
			segments = append(segments, ur.TargetFolder)
		}
		if ur.UseSubmissionFolders {
			segments = append(segments, submissionID)
		}
		segments = append(segments, sanitizeFilename(filename))
		nodePath := strings.Join(segments, "/")

		// Infer content type from filename extension.
		nodeCT := mime.TypeByExtension(path.Ext(filename))

		refStr := string(ref)
		parentID, err := h.ensureParents(ctx, c.ID, nodePath)
		if err != nil {
			return fmt.Errorf("materializing folders for %q: %w", filename, err)
		}
		node := &store.Node{
			CollectionID: c.ID, ParentID: parentID, Path: nodePath,
			Name: path.Base(nodePath), Kind: store.KindFile,
			BlobID: &refStr, Size: &size, Checksum: &sum,
			Metadata: metaJSON,
		}
		if nodeCT != "" {
			node.ContentType = &nodeCT
		}
		if err := h.store.UpsertNode(ctx, node); err != nil {
			return fmt.Errorf("upserting node for %q: %w", filename, err)
		}
		if err := h.store.RecomputeNodeDenormalized(ctx, node.ID); err != nil {
			return fmt.Errorf("recomputing node for %q: %w", filename, err)
		}

		totalBytes += size

		// Build public URL if uploader access is enabled and visibility allows it.
		var fileURL *string
		if ur.AllowUploaderAccess {
			if vis := effectiveVisibility(fileMeta); vis == meta.VisibilityPublic || vis == meta.VisibilityUnlisted {
				u := strings.TrimRight(h.baseURL, "/") + "/c/" + c.Name + "/" + nodePath
				fileURL = &u
			}
		}
		uploaded = append(uploaded, uploadedFile{
			Filename: filename,
			Path:     nodePath,
			Size:     size,
			URL:      fileURL,
		})
		return nil
	}

	if mediaType == "multipart/form-data" {
		r.Body = http.MaxBytesReader(w, r.Body, maxPerFile*int64(maxBodyFiles+1))
		mr, err := r.MultipartReader()
		if err != nil {
			writeJSONError(w, http.StatusBadRequest, "invalid multipart body")
			return
		}
		for {
			part, err := mr.NextPart()
			if errors.Is(err, io.EOF) {
				break
			}
			if err != nil {
				writeJSONError(w, http.StatusBadRequest, "reading multipart: "+err.Error())
				return
			}
			formName := part.FormName()
			if formName != "file" && formName != "files" && part.FileName() == "" {
				_ = part.Close()
				continue
			}
			filename := part.FileName()
			if filename == "" {
				filename = "file"
			}
			if err := processFile(filename, part); err != nil {
				var oe *oversizeError
				if errors.As(err, &oe) {
					writeJSONError(w, http.StatusRequestEntityTooLarge, "file "+oe.filename+" exceeds the size limit")
					return
				}
				if h.log != nil {
					h.log.Error("upload request: process file", "error", err)
				}
				writeJSONError(w, http.StatusInternalServerError, "uploading "+filename)
				return
			}
			_ = part.Close()
		}
	} else {
		// Single-file raw body upload.
		filename := filenameFromDisposition(r.Header.Get("Content-Disposition"))
		if filename == "" {
			filename = "file"
		}
		r.Body = http.MaxBytesReader(w, r.Body, maxPerFile+1)
		if err := processFile(filename, r.Body); err != nil {
			var oe *oversizeError
			if errors.As(err, &oe) {
				writeJSONError(w, http.StatusRequestEntityTooLarge, "file exceeds the size limit")
				return
			}
			if h.log != nil {
				h.log.Error("upload request: process raw file", "error", err)
			}
			writeJSONError(w, http.StatusInternalServerError, "uploading file")
			return
		}
	}

	if len(uploaded) == 0 {
		writeJSONError(w, http.StatusBadRequest, "no files received")
		return
	}

	// Commit the submission atomically (increments counter, auto-closes if single_use).
	sub := &store.UploadRequestSubmission{
		UploadRequestID: ur.ID,
		SubmissionID:    submissionID,
		FileCount:       int64(len(uploaded)),
		TotalBytes:      totalBytes,
		ClientIP:        &ip,
	}
	if err := h.store.CommitUploadRequestSubmission(ctx, sub, ur.SingleUse, time.Now().UTC()); err != nil {
		switch {
		case errors.Is(err, store.ErrUploadRequestNotActive):
			// Race: became inactive between lookupActive and CommitSubmission.
			writeJSONError(w, http.StatusGone, "upload link is no longer active")
		case errors.Is(err, store.ErrUploadRequestQuotaExceeded):
			writeJSONError(w, http.StatusConflict, "upload link has reached its file limit")
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

	writeJSON(w, http.StatusOK, map[string]any{
		"submission_id":   submissionID,
		"files":           uploaded,
		"uploader_access": ur.AllowUploaderAccess,
	})
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

// ensureParents materializes folder nodes for each ancestor of p, returning
// the immediate parent node ID (nil at collection root). Mirrors
// api.Handler.ensureParents (design/15 §4).
func (h *Handler) ensureParents(ctx context.Context, collectionID int64, p string) (*int64, error) {
	segs := strings.Split(p, "/")
	if len(segs) == 1 {
		return nil, nil
	}
	var parentID *int64
	prefix := ""
	for _, seg := range segs[:len(segs)-1] {
		if prefix == "" {
			prefix = seg
		} else {
			prefix += "/" + seg
		}
		existing, err := h.store.GetNode(ctx, collectionID, prefix)
		if err == nil {
			if existing.Kind != store.KindFolder {
				return nil, store.ErrKindConflict
			}
			parentID = &existing.ID
			continue
		}
		if !errors.Is(err, store.ErrNotFound) {
			return nil, err
		}
		folder := &store.Node{
			CollectionID: collectionID, ParentID: parentID, Path: prefix,
			Name: path.Base(prefix), Kind: store.KindFolder, Metadata: json.RawMessage(`{}`),
		}
		if err := h.store.UpsertNode(ctx, folder); err != nil {
			return nil, err
		}
		id := folder.ID
		parentID = &id
	}
	return parentID, nil
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

func effectiveVisibility(doc *meta.Document) string {
	if doc == nil || doc.Access == nil || doc.Access.Visibility == nil {
		return meta.VisibilityPublic
	}
	return *doc.Access.Visibility
}

func strVal(s *string) string {
	if s == nil {
		return ""
	}
	return *s
}

func clientIP(r *http.Request) string {
	if xff := r.Header.Get("X-Forwarded-For"); xff != "" {
		return strings.SplitN(xff, ",", 2)[0]
	}
	addr := r.RemoteAddr
	if i := strings.LastIndex(addr, ":"); i >= 0 {
		return addr[:i]
	}
	return addr
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

// oversizeError signals that a single file exceeded the per-file size limit.
type oversizeError struct{ filename string }

func (e *oversizeError) Error() string {
	return "file " + e.filename + " exceeds size limit"
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
