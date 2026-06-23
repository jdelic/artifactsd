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
