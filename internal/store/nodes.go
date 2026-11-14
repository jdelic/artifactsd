package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"path"
	"strings"
	"time"
)

const nodeCols = `id, collection_id, parent_id, path, name, kind, blob_id, size,
	content_type, checksum, metadata, created_at, updated_at, last_access_at,
	visibility, expires_at, upload_request_id`

func scanNode(row interface{ Scan(...any) error }) (*Node, error) {
	var (
		n           Node
		parentID    sql.NullInt64
		blobID      sql.NullString
		size        sql.NullInt64
		contentType sql.NullString
		checksum    sql.NullString
		meta        string
		created     string
		updated     string
		lastAccess  sql.NullString
		visibility  sql.NullString
		expires     sql.NullString
		uploadReq   sql.NullInt64
	)
	if err := row.Scan(
		&n.ID, &n.CollectionID, &parentID, &n.Path, &n.Name, &n.Kind,
		&blobID, &size, &contentType, &checksum, &meta,
		&created, &updated, &lastAccess, &visibility, &expires, &uploadReq,
	); err != nil {
		return nil, err
	}

	n.ParentID = int64Ptr(parentID)
	n.BlobID = strPtr(blobID)
	n.Size = int64Ptr(size)
	n.ContentType = strPtr(contentType)
	n.Checksum = strPtr(checksum)
	n.Metadata = []byte(meta)
	n.Visibility = strPtr(visibility)
	n.UploadRequestID = int64Ptr(uploadReq)

	var err error
	if n.CreatedAt, err = parseTime(created); err != nil {
		return nil, err
	}
	if n.UpdatedAt, err = parseTime(updated); err != nil {
		return nil, err
	}
	if n.LastAccessAt, err = parseTimePtr(lastAccess); err != nil {
		return nil, err
	}
	if n.ExpiresAt, err = parseTimePtr(expires); err != nil {
		return nil, err
	}
	return &n, nil
}

// UpsertNode inserts a node or updates the existing one with the same
// (collection_id, path), preserving created_at and last_access_at on update —
// except that changing the idle-expiry rule restarts its clock by setting
// last_access_at to now (see idleRuleChanged*SQL).
// The node's ID is populated on return.
//
// It enforces the tree's kind invariants (artifactsd-2bw): the parent (when
// set) must be a folder, and a path's kind may not change between file and
// folder in place — replacing one with the other requires an explicit
// delete/recreate. Violations return ErrKindConflict without mutating any row.
func (s *Store) UpsertNode(ctx context.Context, n *Node) error {
	return s.upsertNode(ctx, s.db.sql, n)
}

// upsertNode is UpsertNode on q (the DB or a transaction).
func (s *Store) upsertNode(ctx context.Context, q querier, n *Node) error {
	exists, err := checkKindInvariants(ctx, q, n)
	if err != nil {
		return err
	}
	if !exists && n.Kind == KindFile {
		if err := s.checkCapacity(ctx, q, n.CollectionID); err != nil {
			return err
		}
	}

	now := s.now()
	if n.CreatedAt.IsZero() {
		n.CreatedAt = now
	}
	n.UpdatedAt = now

	err = q.QueryRowContext(
		ctx,
		`INSERT INTO nodes
			(collection_id, parent_id, path, name, kind, blob_id, size, content_type,
			 checksum, metadata, created_at, updated_at, last_access_at, visibility, expires_at,
			 upload_request_id)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		 ON CONFLICT(collection_id, path) DO UPDATE SET
			parent_id    = excluded.parent_id,
			name         = excluded.name,
			kind         = excluded.kind,
			blob_id      = excluded.blob_id,
			size         = excluded.size,
			content_type = excluded.content_type,
			checksum     = excluded.checksum,
			last_access_at = CASE WHEN `+idleRuleChangedNodeSQL+`
				THEN excluded.updated_at ELSE nodes.last_access_at END,
			metadata     = excluded.metadata,
			updated_at   = excluded.updated_at,
			visibility   = excluded.visibility,
			expires_at   = excluded.expires_at,
			upload_request_id = excluded.upload_request_id
		 RETURNING id`,
		n.CollectionID, n.ParentID, n.Path, n.Name, n.Kind, n.BlobID, n.Size, n.ContentType,
		n.Checksum, defaultJSON(n.Metadata), fmtTime(n.CreatedAt), fmtTime(n.UpdatedAt),
		fmtTimePtr(n.LastAccessAt), n.Visibility, fmtTimePtr(n.ExpiresAt),
		n.UploadRequestID,
	).Scan(&n.ID)
	return err
}

// checkKindInvariants verifies, before an upsert, that the parent is a folder
// and that an existing node at the same path is not flipping kind
// (artifactsd-2bw). It reports whether a node already exists at n's path.
func checkKindInvariants(ctx context.Context, q querier, n *Node) (exists bool, err error) {
	if n.ParentID != nil {
		var parentKind string
		err := q.QueryRowContext(ctx, `SELECT kind FROM nodes WHERE id = ?`, *n.ParentID).Scan(&parentKind)
		if errors.Is(err, sql.ErrNoRows) {
			return false, ErrKindConflict // parent vanished; treat as a structural conflict
		}
		if err != nil {
			return false, err
		}
		if parentKind != KindFolder {
			return false, ErrKindConflict // a file cannot be a parent
		}
	}

	var existingKind string
	err = q.QueryRowContext(ctx,
		`SELECT kind FROM nodes WHERE collection_id = ? AND path = ?`, n.CollectionID, n.Path).Scan(&existingKind)
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil // new path, no conflict
	}
	if err != nil {
		return false, err
	}
	if existingKind != n.Kind {
		return true, ErrKindConflict // file<->folder replacement is not allowed in place
	}
	return true, nil
}

// CountFiles returns the number of file nodes in a collection.
func (s *Store) CountFiles(ctx context.Context, collectionID int64) (int, error) {
	return countFiles(ctx, s.db.sql, collectionID)
}

func countFiles(ctx context.Context, q querier, collectionID int64) (int, error) {
	var n int
	err := q.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM nodes WHERE collection_id = ? AND kind = ?`, collectionID, KindFile).Scan(&n)
	return n, err
}

// checkCapacity returns ErrCollectionFull when the collection already holds
// the maximum number of files. Inside a transaction (the upload-link commit)
// the check and the insert are atomic. On the token-authenticated paths, which
// call UpsertNode outside a transaction, two concurrent creators can overshoot
// the cap by a few files; that is acceptable for credentialed writers.
func (s *Store) checkCapacity(ctx context.Context, q querier, collectionID int64) error {
	limit := s.MaxFilesPerCollection()
	if limit == 0 {
		return nil
	}
	n, err := countFiles(ctx, q, collectionID)
	if err != nil {
		return err
	}
	if n >= limit {
		return ErrCollectionFull
	}
	return nil
}

// CheckCollectionCapacity is an early, advisory check for request handlers:
// it returns ErrCollectionFull when creating a NEW file at path would exceed
// the cap. Replacing an existing file is always allowed. The authoritative
// check happens again when the node is written.
func (s *Store) CheckCollectionCapacity(ctx context.Context, collectionID int64, path string) error {
	if s.MaxFilesPerCollection() == 0 {
		return nil
	}
	existing, err := s.GetNode(ctx, collectionID, path)
	if err == nil && existing.Kind == KindFile {
		return nil
	}
	if err != nil && !errors.Is(err, ErrNotFound) {
		return err
	}
	return s.checkCapacity(ctx, s.db.sql, collectionID)
}

// GetNode returns a node by (collection, path).
func (s *Store) GetNode(ctx context.Context, collectionID int64, path string) (*Node, error) {
	return getNode(ctx, s.db.sql, collectionID, path)
}

func getNode(ctx context.Context, q querier, collectionID int64, path string) (*Node, error) {
	row := q.QueryRowContext(ctx,
		`SELECT `+nodeCols+` FROM nodes WHERE collection_id = ? AND path = ?`, collectionID, path)
	n, err := scanNode(row)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	return n, err
}

// GetNodeByID returns a node by id.
func (s *Store) GetNodeByID(ctx context.Context, id int64) (*Node, error) {
	return getNodeByID(ctx, s.db.sql, id)
}

func getNodeByID(ctx context.Context, q querier, id int64) (*Node, error) {
	row := q.QueryRowContext(ctx, `SELECT `+nodeCols+` FROM nodes WHERE id = ?`, id)
	n, err := scanNode(row)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	return n, err
}

// FileWithCollection is a file node plus its collection name, for the admin
// cross-collection file browser (artifactsd-0ab).
type FileWithCollection struct {
	CollectionName string
	Path           string
	Name           string
	Size           int64
	ContentType    string
	CreatedAt      time.Time
	ExpiresAt      *time.Time // the file's own (denormalized) expiry; nil = never
	// Effective is when the file is actually deleted: the earliest of its own,
	// any ancestor folder's, and its collection's expiry (artifactsd-3wy).
	Effective EffectiveExpiry
}

// FileSort selects the ordering of ListAllFiles.
type FileSort string

const (
	// FileSortSize orders by descending size (largest first) — the default.
	FileSortSize FileSort = "size"
	// FileSortName orders by collection then path.
	FileSortName FileSort = "name"
	// FileSortCreated orders by newest first.
	FileSortCreated FileSort = "created"
	// FileSortExpiry orders by soonest effective expiry first; never-expiring
	// files last.
	FileSortExpiry FileSort = "expiry"
)

// allFilesQuery selects a complete, constant SQL string per sort order, so the
// ORDER BY is never built from a runtime value (no injection surface). SQLite
// treats LIMIT -1 as "no limit", so the caller can pass a negative limit for an
// uncapped listing.
//
// Besides the file's own expiry it returns the collection's and the
// earliest-expiring ancestor folder's (a correlated subquery over folders
// that have an expiry, which are few — idx_nodes_expiry), and eff, their
// minimum, so "expiring next" sorts by when files are really deleted. eff is
// a text minimum over RFC3339 strings; mixed fractional-second precision can
// misorder two instants within the same second, which is harmless for a sort.
// The precise minimum and its source are recomputed in Go.
const allFilesQueryBase = `WITH f AS (
	SELECT c.name AS cname, n.collection_id AS cid, n.path, n.name,
		COALESCE(n.size, 0) AS size, COALESCE(n.content_type, '') AS ctype,
		n.created_at, n.expires_at AS nexp, n.metadata AS nmeta, n.last_access_at AS nlast,
		c.expires_at AS cexp, c.metadata AS cmeta, c.created_at AS ccreated, c.last_access_at AS clast,
		(SELECT a.path FROM nodes a
		  WHERE a.collection_id = n.collection_id AND a.kind = 'folder' AND a.expires_at IS NOT NULL
		    AND substr(n.path, 1, length(a.path) + 1) = a.path || '/'
		  ORDER BY a.expires_at, length(a.path) LIMIT 1) AS apath
	FROM nodes n JOIN collections c ON c.id = n.collection_id
	WHERE n.kind = ?
), g AS (
	SELECT f.*, (SELECT a.expires_at FROM nodes a WHERE a.collection_id = f.cid AND a.path = f.apath) AS aexp
	FROM f
)
SELECT cname, cid, path, name, size, ctype, created_at, nexp, nmeta, nlast,
	cexp, cmeta, ccreated, clast, apath,
	NULLIF(min(COALESCE(nexp, '~'), COALESCE(cexp, '~'), COALESCE(aexp, '~')), '~') AS eff
FROM g ORDER BY `

const (
	allFilesBySize    = allFilesQueryBase + "size DESC, cname, path LIMIT ?"
	allFilesByName    = allFilesQueryBase + "cname, path LIMIT ?"
	allFilesByCreated = allFilesQueryBase + "created_at DESC LIMIT ?"
	// Soonest-expiring first; NULL (never expires) sorts last.
	allFilesByExpiry = allFilesQueryBase + "eff IS NULL, eff ASC, cname, path LIMIT ?"
)

// ListAllFiles returns every file node (folders excluded) across all
// collections, ordered per sort (default: largest first), up to limit rows
// (limit <= 0 means no cap). Each row carries its collection name, its own
// expiry, and its effective expiry with the source. Beyond the main query it
// costs one query per collection that has a folder-sourced expiry in the
// result (to label that folder's rule), usually none.
func (s *Store) ListAllFiles(ctx context.Context, sortBy FileSort, limit int) ([]*FileWithCollection, error) {
	query := allFilesBySize
	switch sortBy {
	case FileSortName:
		query = allFilesByName
	case FileSortCreated:
		query = allFilesByCreated
	case FileSortExpiry:
		query = allFilesByExpiry
	}
	if limit <= 0 {
		limit = -1 // SQLite: no limit
	}
	rows, err := s.db.sql.QueryContext(ctx, query, KindFile, limit)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()

	type pending struct {
		f      *FileWithCollection
		own    EffectiveExpiry
		coll   EffectiveExpiry
		cid    int64
		folder string
	}
	var (
		out     []*FileWithCollection
		pend    []*pending
		folders = map[int64][]string{} // collection id → folder paths to label
	)
	for rows.Next() {
		var (
			f                                     FileWithCollection
			cid                                   int64
			created, nmeta, cmeta, ccreated       string
			nexp, nlast, cexp, clast, apath, effS sql.NullString
		)
		if err := rows.Scan(&f.CollectionName, &cid, &f.Path, &f.Name, &f.Size, &f.ContentType, &created,
			&nexp, &nmeta, &nlast, &cexp, &cmeta, &ccreated, &clast, &apath, &effS); err != nil {
			return nil, err
		}
		var perr error
		if f.CreatedAt, perr = parseTime(created); perr != nil {
			return nil, perr
		}
		if f.ExpiresAt, perr = parseTimePtr(nexp); perr != nil {
			return nil, perr
		}
		nLast, perr := parseTimePtr(nlast)
		if perr != nil {
			return nil, perr
		}
		cExp, perr := parseTimePtr(cexp)
		if perr != nil {
			return nil, perr
		}
		cCreated, perr := parseTime(ccreated)
		if perr != nil {
			return nil, perr
		}
		cLast, perr := parseTimePtr(clast)
		if perr != nil {
			return nil, perr
		}
		p := &pending{
			f:    &f,
			own:  s.ownExpiry(ExpiryOwn, f.Path, f.ExpiresAt, json.RawMessage(nmeta), f.CreatedAt, nLast),
			coll: s.ownExpiry(ExpiryCollection, "", cExp, json.RawMessage(cmeta), cCreated, cLast),
			cid:  cid,
		}
		if apath.Valid {
			p.folder = apath.String
			folders[cid] = append(folders[cid], apath.String)
		}
		pend = append(pend, p)
		out = append(out, &f)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}

	folderNodes := map[int64]map[string]*Node{}
	for cid, ps := range folders {
		m, err := s.getNodesByPaths(ctx, cid, ps)
		if err != nil {
			return nil, err
		}
		folderNodes[cid] = m
	}
	for _, p := range pend {
		eff := p.coll
		if n := folderNodes[p.cid][p.folder]; n != nil {
			eff = earlierOf(eff, s.ownExpiry(ExpiryFolder, n.Path, n.ExpiresAt, n.Metadata, n.CreatedAt, n.LastAccessAt))
		}
		p.f.Effective = earlierOf(eff, p.own)
	}
	return out, nil
}

// ListChildren returns the direct children of a parent node within a
// collection. A nil parentID lists the collection's root-level nodes.
func (s *Store) ListChildren(ctx context.Context, collectionID int64, parentID *int64) ([]*Node, error) {
	var (
		rows *sql.Rows
		err  error
	)
	if parentID == nil {
		rows, err = s.db.sql.QueryContext(ctx,
			`SELECT `+nodeCols+` FROM nodes WHERE collection_id = ? AND parent_id IS NULL ORDER BY name`,
			collectionID)
	} else {
		rows, err = s.db.sql.QueryContext(ctx,
			`SELECT `+nodeCols+` FROM nodes WHERE collection_id = ? AND parent_id = ? ORDER BY name`,
			collectionID, *parentID)
	}
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()

	return collectNodes(rows)
}

// ListCollectionNodes returns all nodes in a collection ordered by path.
func (s *Store) ListCollectionNodes(ctx context.Context, collectionID int64) ([]*Node, error) {
	rows, err := s.db.sql.QueryContext(ctx,
		`SELECT `+nodeCols+` FROM nodes WHERE collection_id = ? ORDER BY path`, collectionID)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	return collectNodes(rows)
}

func collectNodes(rows *sql.Rows) ([]*Node, error) {
	var out []*Node
	for rows.Next() {
		n, err := scanNode(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, n)
	}
	return out, rows.Err()
}

// EnsureParentFolders materializes folder nodes for each ancestor of p,
// returning the immediate parent's id (nil at collection root) — design/07
// §4: always create parent folder rows on upload. An existing non-folder
// ancestor returns ErrKindConflict. Shared by the API and admin upload paths
// (previously duplicated in both packages).
func (s *Store) EnsureParentFolders(ctx context.Context, collectionID int64, p string) (*int64, error) {
	return s.ensureParentFolders(ctx, s.db.sql, collectionID, p)
}

// ensureParentFolders is EnsureParentFolders on q (the DB or a transaction).
func (s *Store) ensureParentFolders(ctx context.Context, q querier, collectionID int64, p string) (*int64, error) {
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
		existing, err := getNode(ctx, q, collectionID, prefix)
		if err == nil {
			if existing.Kind != KindFolder {
				return nil, ErrKindConflict // an existing ancestor must be a folder
			}
			parentID = &existing.ID
			continue
		}
		if !errors.Is(err, ErrNotFound) {
			return nil, err
		}
		folder := &Node{
			CollectionID: collectionID, ParentID: parentID, Path: prefix,
			Name: path.Base(prefix), Kind: KindFolder, Metadata: json.RawMessage(`{}`),
		}
		if err := s.upsertNode(ctx, q, folder); err != nil {
			return nil, err
		}
		id := folder.ID
		parentID = &id
	}
	return parentID, nil
}

// DeleteNode removes a node and (via parent_id cascade) its descendants.
func (s *Store) DeleteNode(ctx context.Context, id int64) error {
	res, err := s.db.sql.ExecContext(ctx, `DELETE FROM nodes WHERE id = ?`, id)
	if err != nil {
		return err
	}
	return requireOne(res)
}

// TouchLastAccess sets last_access_at on a node.
func (s *Store) TouchLastAccess(ctx context.Context, id int64, at time.Time) error {
	res, err := s.db.sql.ExecContext(ctx,
		`UPDATE nodes SET last_access_at = ? WHERE id = ?`, fmtTime(at), id)
	if err != nil {
		return err
	}
	return requireOne(res)
}
