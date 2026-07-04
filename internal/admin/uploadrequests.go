package admin

import (
	"encoding/json"
	"errors"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/jdelic/artifactsd/internal/audit"
	"github.com/jdelic/artifactsd/internal/auth"
	"github.com/jdelic/artifactsd/internal/durations"
	"github.com/jdelic/artifactsd/internal/session"
	"github.com/jdelic/artifactsd/internal/store"
	"github.com/jdelic/artifactsd/internal/units"
)

// uploadRequestRow is one row of the upload requests table.
type uploadRequestRow struct {
	RequestID     string
	URL           string
	Collection    string
	TargetFolder  string
	Label         string
	Status        string
	SingleUse     bool
	FilesUploaded int64
	CreatedAt     string
	ExpiresAt     string
}

// uploadRequestsPanelData backs the "uploadrequests_panel" partial.
type uploadRequestsPanelData struct {
	AdminPath   string
	Collections []string
	Requests    []uploadRequestRow
	NewURL      string
	Error       string
}

var uploadRequestsFKeys = []fkey{{"Alt-C", "Create"}, {"Alt-U", "Upload Requests"}, {"Alt-L", "Logout"}}

// allUploadRequests renders the cross-collection upload requests screen (design/15 §6).
func (h *Handler) allUploadRequests(w http.ResponseWriter, r *http.Request) {
	data, err := h.loadUploadRequestsPanel(r, "", "")
	if err != nil {
		http.Error(w, "loading upload requests: "+err.Error(), http.StatusInternalServerError)
		return
	}
	h.render(w, r, "uploadrequests", uploadRequestsFKeys, "READY", "Upload Request Links", data)
}

// createUploadRequest mints a new upload request link (design/15 §5).
func (h *Handler) createUploadRequestAdmin(w http.ResponseWriter, r *http.Request) {
	collection := r.FormValue("collection")
	if collection == "" {
		h.renderUploadRequestsPanel(w, r, "", "collection is required")
		return
	}
	c, ok := h.lookupCollectionByName(w, r, collection)
	if !ok {
		return
	}

	ctx := r.Context()
	requestID, err := auth.GenerateUploadRequestID()
	if err != nil {
		http.Error(w, "generating upload request id: "+err.Error(), http.StatusInternalServerError)
		return
	}

	targetFolder := strings.TrimSpace(r.FormValue("target_folder"))
	label := strings.TrimSpace(r.FormValue("label"))
	description := strings.TrimSpace(r.FormValue("description"))

	ur := &store.UploadRequest{
		RequestID:    requestID,
		CollectionID: c.ID,
		TargetFolder: targetFolder,
		FileMetadata: []byte(`{}`),
	}
	if label != "" {
		ur.Label = &label
	}
	if description != "" {
		ur.Description = &description
	}

	// The absolute/relative expiry pair mirrors the CLI's --expires /
	// --expires-in convention (design/10 §2).
	expiresAt := strings.TrimSpace(r.FormValue("expires_at"))
	expiresIn := strings.TrimSpace(r.FormValue("expires_in"))
	switch {
	case expiresAt != "" && expiresIn != "":
		h.renderUploadRequestsPanel(w, r, "", "set either an absolute expiry or a duration, not both")
		return
	case expiresAt != "":
		t, err := time.Parse(time.RFC3339, expiresAt)
		if err != nil {
			h.renderUploadRequestsPanel(w, r, "", "invalid expires_at (want RFC3339): "+expiresAt)
			return
		}
		ur.ExpiresAt = &t
	case expiresIn != "":
		d, err := durations.Parse(expiresIn)
		if err != nil {
			h.renderUploadRequestsPanel(w, r, "", "invalid expires_in (want a duration like 7d): "+expiresIn)
			return
		}
		t := time.Now().UTC().Add(d)
		ur.ExpiresAt = &t
	}

	if r.FormValue("single_use") == "on" {
		ur.SingleUse = true
	}
	if r.FormValue("use_submission_folders") == "on" {
		ur.UseSubmissionFolders = true
	}
	if r.FormValue("allow_uploader_access") == "on" {
		ur.AllowUploaderAccess = true
	}
	if v := strings.TrimSpace(r.FormValue("max_files")); v != "" {
		n, err := strconv.ParseInt(v, 10, 64)
		if err != nil || n < 1 {
			h.renderUploadRequestsPanel(w, r, "", "invalid max files: "+v)
			return
		}
		ur.MaxFiles = &n
	}
	if v := strings.TrimSpace(r.FormValue("max_file_size")); v != "" {
		sz, err := units.ParseByteSize(v)
		if err != nil {
			h.renderUploadRequestsPanel(w, r, "", "invalid max file size: "+err.Error())
			return
		}
		if sz > h.cfg.Limits.MaxUploadSize {
			h.renderUploadRequestsPanel(w, r, "", "max file size exceeds the server upload limit")
			return
		}
		ur.MaxFileSize = &sz
	}
	if vis := r.FormValue("file_visibility"); vis != "" {
		switch vis {
		case "public", "unlisted", "protected", "private":
			meta, err := json.Marshal(map[string]any{"access": map[string]any{"visibility": vis}})
			if err != nil {
				http.Error(w, "encoding file metadata: "+err.Error(), http.StatusInternalServerError)
				return
			}
			ur.FileMetadata = meta
		default:
			h.renderUploadRequestsPanel(w, r, "", "invalid file visibility: "+vis)
			return
		}
	}

	if err := h.store.CreateUploadRequest(ctx, ur); err != nil {
		h.renderUploadRequestsPanel(w, r, "", "creating upload request: "+err.Error())
		return
	}
	if h.audit != nil {
		id := session.FromContext(ctx)
		_ = h.audit.Record(ctx, &audit.Entry{
			Action: audit.ActionUploadRequestCreate,
			Actor:  actorOf(id),
			Target: c.Name + " " + requestID,
		})
	}
	h.renderUploadRequestsPanel(w, r, h.uploadRequestURL(requestID), "")
}

// revokeUploadRequestAdmin revokes an upload request link (design/15 §5).
func (h *Handler) revokeUploadRequestAdmin(w http.ResponseWriter, r *http.Request) {
	requestID := r.PathValue("request_id")
	ctx := r.Context()

	ur, err := h.store.GetUploadRequest(ctx, requestID)
	if errors.Is(err, store.ErrNotFound) {
		h.renderUploadRequestsPanel(w, r, "", "upload request does not exist")
		return
	}
	if err != nil {
		http.Error(w, "looking up upload request: "+err.Error(), http.StatusInternalServerError)
		return
	}

	if err := h.store.CloseUploadRequest(ctx, ur.RequestID, store.UploadRequestRevoked, time.Now().UTC()); err != nil {
		if !errors.Is(err, store.ErrNotFound) {
			h.renderUploadRequestsPanel(w, r, "", "revoking upload request: "+err.Error())
			return
		}
	}
	if h.audit != nil {
		id := session.FromContext(ctx)
		_ = h.audit.Record(ctx, &audit.Entry{
			Action: audit.ActionUploadRequestRevoke,
			Actor:  actorOf(id),
			Target: ur.RequestID,
		})
	}
	h.renderUploadRequestsPanel(w, r, "", "")
}

func (h *Handler) renderUploadRequestsPanel(w http.ResponseWriter, r *http.Request, newURL, errMsg string) {
	data, err := h.loadUploadRequestsPanel(r, newURL, errMsg)
	if err != nil {
		http.Error(w, "loading upload requests: "+err.Error(), http.StatusInternalServerError)
		return
	}
	h.renderPartial(w, "uploadrequests_panel", data)
}

func (h *Handler) loadUploadRequestsPanel(r *http.Request, newURL, errMsg string) (uploadRequestsPanelData, error) {
	ctx := r.Context()

	urs, err := h.store.ListUploadRequests(ctx, nil)
	if err != nil {
		return uploadRequestsPanelData{}, err
	}

	cols, err := h.store.ListCollections(ctx)
	if err != nil {
		return uploadRequestsPanelData{}, err
	}
	colNames := make(map[int64]string, len(cols))
	colList := make([]string, 0, len(cols))
	for _, c := range cols {
		colNames[c.ID] = c.Name
		colList = append(colList, c.Name)
	}

	rows := make([]uploadRequestRow, 0, len(urs))
	for _, ur := range urs {
		row := uploadRequestRow{
			RequestID:     ur.RequestID,
			URL:           h.uploadRequestURL(ur.RequestID),
			Collection:    colNames[ur.CollectionID],
			TargetFolder:  ur.TargetFolder,
			Status:        ur.Status,
			SingleUse:     ur.SingleUse,
			FilesUploaded: ur.FilesUploaded,
			CreatedAt:     ur.CreatedAt.UTC().Format("2006-01-02 15:04"),
		}
		if ur.Label != nil {
			row.Label = *ur.Label
		}
		if ur.ExpiresAt != nil {
			row.ExpiresAt = ur.ExpiresAt.UTC().Format("2006-01-02 15:04")
		}
		rows = append(rows, row)
	}

	return uploadRequestsPanelData{
		AdminPath:   h.cfg.Admin.Path,
		Collections: colList,
		Requests:    rows,
		NewURL:      newURL,
		Error:       errMsg,
	}, nil
}

func (h *Handler) uploadRequestURL(requestID string) string {
	return strings.TrimRight(h.cfg.Server.BaseURL, "/") + "/r/" + requestID
}
