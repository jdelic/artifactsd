package store

import (
	"encoding/json"
	"time"
)

// Node kinds.
const (
	KindFile   = "file"
	KindFolder = "folder"
)

// Upload session statuses.
const (
	UploadPending   = "pending"
	UploadCompleted = "completed"
	UploadAborted   = "aborted"
	UploadExpired   = "expired"
)

// Collection is a top-level published namespace (design/02 §1).
type Collection struct {
	ID         int64
	Name       string
	Metadata   json.RawMessage
	CreatedAt  time.Time
	UpdatedAt  time.Time
	Visibility string     // denormalized effective visibility
	ExpiresAt  *time.Time // denormalized effective expiry
}

// Node is a file or folder within a collection.
type Node struct {
	ID           int64
	CollectionID int64
	ParentID     *int64
	Path         string
	Name         string
	Kind         string
	// File-only fields (nil for folders):
	BlobID      *string
	Size        *int64
	ContentType *string
	Checksum    *string

	Metadata     json.RawMessage
	CreatedAt    time.Time
	UpdatedAt    time.Time
	LastAccessAt *time.Time
	// Denormalized (nil visibility = inherit):
	Visibility *string
	ExpiresAt  *time.Time
}

// Share is an unguessable-URL grant to a collection subtree (design/02 §2).
type Share struct {
	ID           int64
	ShareID      string
	CollectionID int64
	TargetPath   string
	TargetKind   string
	Metadata     json.RawMessage
	CreatedAt    time.Time
	ExpiresAt    *time.Time
	RevokedAt    *time.Time
	LastAccessAt *time.Time
}

// UploadSession tracks an in-progress direct/large upload.
type UploadSession struct {
	ID           int64
	UploadID     string
	CollectionID int64
	TargetPath   string
	Metadata     json.RawMessage
	ContentType  *string
	Status       string
	TempBlobID   *string
	CreatedAt    time.Time
	ExpiresAt    time.Time
	CompletedAt  *time.Time
}

// APIToken is a management/admin credential; only the hash is stored.
type APIToken struct {
	ID          int64
	Name        string
	TokenHash   string
	TokenPrefix string
	Scopes      json.RawMessage
	CreatedAt   time.Time
	LastUsedAt  *time.Time
	ExpiresAt   *time.Time
	RevokedAt   *time.Time
}

// Upload request statuses (design/15 §3).
const (
	UploadRequestActive  = "active"
	UploadRequestClosed  = "closed"
	UploadRequestRevoked = "revoked"
	UploadRequestExpired = "expired"
)

// UploadRequest is a shareable capability link that allows third-party file
// uploads to a designated collection/folder (design/15).
type UploadRequest struct {
	ID                   int64
	RequestID            string
	CollectionID         int64
	TargetFolder         string
	Label                *string
	Description          *string
	FileMetadata         json.RawMessage
	AllowUploaderAccess  bool
	SingleUse            bool
	UseSubmissionFolders bool
	MaxFiles             *int64
	MaxFileSize          *int64
	FilesUploaded        int64
	Status               string
	CreatedAt            time.Time
	ExpiresAt            *time.Time
	RevokedAt            *time.Time
	LastUsedAt           *time.Time
}

// IsActive reports whether the request can accept new uploads.
func (r *UploadRequest) IsActive(now time.Time) bool {
	if r.Status != UploadRequestActive {
		return false
	}
	if r.ExpiresAt != nil && !now.Before(*r.ExpiresAt) {
		return false
	}
	return true
}

// UploadRequestSubmission tracks one upload event (one POST with ≥1 file) to a
// request link. Used for admin analytics and for finding which files belong to
// which submission when use_submission_folders=true.
type UploadRequestSubmission struct {
	ID              int64
	UploadRequestID int64
	SubmissionID    string
	FileCount       int64
	TotalBytes      int64
	ClientIP        *string
	CreatedAt       time.Time
}

// AuditLog is an admin/deletion action record (SPEC §11/§17).
type AuditLog struct {
	ID        int64
	Timestamp time.Time
	Actor     *string
	Action    string
	Target    *string
	Details   json.RawMessage
	RequestID *string
}
