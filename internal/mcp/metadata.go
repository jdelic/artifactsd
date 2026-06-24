package mcp

import (
	"encoding/json"
	"fmt"

	"github.com/jdelic/artifactsd/pkg/apiclient"
)

// decodeMetadata parses a metadata document into a plain map for tool output.
// Tool Out types use map[string]any rather than json.RawMessage for metadata
// documents: json.RawMessage's underlying []byte makes the SDK's reflection-
// based schema inference describe the field as an array of byte integers
// instead of an object, which then fails the SDK's own output validation
// against that (wrong) schema at runtime.
func decodeMetadata(raw json.RawMessage) (map[string]any, error) {
	if len(raw) == 0 {
		return nil, nil
	}
	var m map[string]any
	if err := json.Unmarshal(raw, &m); err != nil {
		return nil, fmt.Errorf("decoding metadata: %w", err)
	}
	return m, nil
}

// validVisibility reports whether v is a recognized visibility value, or empty
// (meaning "use the default").
func validVisibility(v string) bool {
	switch v {
	case "", "public", "unlisted", "protected", "private":
		return true
	default:
		return false
	}
}

// buildCollectionMetadata assembles a collection metadata document from
// artifact_create_collection's optional policy fields.
func buildCollectionMetadata(visibility string, directoryListing, crawlable, indexable *bool) (json.RawMessage, error) {
	if !validVisibility(visibility) {
		return nil, fmt.Errorf("invalid visibility %q: want public, unlisted, protected, or private", visibility)
	}
	access := map[string]any{}
	if visibility != "" {
		access["visibility"] = visibility
	}
	if directoryListing != nil {
		access["directory_listing"] = *directoryListing
	}
	crawl := map[string]any{}
	if crawlable != nil {
		crawl["crawlable"] = *crawlable
	}
	if indexable != nil {
		crawl["indexable"] = *indexable
	}
	doc := map[string]any{}
	if len(access) > 0 {
		doc["access"] = access
	}
	if len(crawl) > 0 {
		doc["crawl"] = crawl
	}
	if len(doc) == 0 {
		return nil, nil
	}
	return json.Marshal(doc)
}

// CollectionOut mirrors apiclient.Collection with Metadata decoded to a plain
// object (see decodeMetadata).
type CollectionOut struct {
	Name       string         `json:"name"`
	Metadata   map[string]any `json:"metadata,omitempty"`
	Visibility string         `json:"visibility"`
	CreatedAt  string         `json:"created_at"`
	UpdatedAt  string         `json:"updated_at"`
	ExpiresAt  *string        `json:"expires_at,omitempty"`
}

func toCollectionOut(c *apiclient.Collection) (CollectionOut, error) {
	meta, err := decodeMetadata(c.Metadata)
	if err != nil {
		return CollectionOut{}, err
	}
	return CollectionOut{
		Name: c.Name, Metadata: meta, Visibility: c.Visibility,
		CreatedAt: c.CreatedAt, UpdatedAt: c.UpdatedAt, ExpiresAt: c.ExpiresAt,
	}, nil
}

// FileMetaOut mirrors apiclient.FileMeta with Own/Effective decoded to plain
// objects (see decodeMetadata).
type FileMetaOut struct {
	Path      string         `json:"path"`
	Own       map[string]any `json:"own,omitempty"`
	Effective map[string]any `json:"effective,omitempty"`
}

func toFileMetaOut(m *apiclient.FileMeta) (FileMetaOut, error) {
	own, err := decodeMetadata(m.Own)
	if err != nil {
		return FileMetaOut{}, fmt.Errorf("own metadata: %w", err)
	}
	eff, err := decodeMetadata(m.Effective)
	if err != nil {
		return FileMetaOut{}, fmt.Errorf("effective metadata: %w", err)
	}
	return FileMetaOut{Path: m.Path, Own: own, Effective: eff}, nil
}
