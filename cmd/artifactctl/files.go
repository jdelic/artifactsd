package main

import (
	"encoding/json"
	"fmt"
	"io"
	"mime"
	"os"
	"path/filepath"
	"strings"

	"github.com/spf13/cobra"

	"github.com/jdelic/artifactsd/pkg/apiclient"
)

func (a *app) newPutCmd() *cobra.Command {
	var (
		contentType string
		metaFlags   uploadMetaFlags
	)
	cmd := &cobra.Command{
		Use:   "put <collection>/<path> <localfile>",
		Short: "Upload a file",
		Args:  exactArgs(2, "<collection>/<path> <localfile>"),
		RunE: func(cmd *cobra.Command, args []string) error {
			cli, err := a.client()
			if err != nil {
				return err
			}
			collection, path, err := splitTarget(args[0])
			if err != nil {
				return err
			}
			doc, err := metaFlags.document()
			if err != nil {
				return err
			}
			f, err := os.Open(args[1]) //nolint:gosec // user-supplied local file to upload
			if err != nil {
				return err
			}
			defer func() { _ = f.Close() }()

			ct := contentType
			if ct == "" {
				ct = mime.TypeByExtension(filepath.Ext(args[1]))
			}
			info, err := cli.PutFile(cmd.Context(), collection, path, f, apiclient.PutFileOptions{ContentType: ct, Metadata: doc})
			if err != nil {
				return err
			}
			a.printGeneratedToken(metaFlags.auth.genToken)
			return a.renderFile(info, "uploaded %s (%d bytes)\n")
		},
	}
	cmd.Flags().StringVar(&contentType, "content-type", "", "content type (default: inferred from extension)")
	metaFlags.register(cmd.Flags())
	return cmd
}

func (a *app) newGetCmd() *cobra.Command {
	var outFile string
	cmd := &cobra.Command{
		Use:   "get <collection>/<path>",
		Short: "Download a file",
		Args:  exactArgs(1, "<collection>/<path>"),
		RunE: func(cmd *cobra.Command, args []string) error {
			cli, err := a.client()
			if err != nil {
				return err
			}
			collection, path, err := splitTarget(args[0])
			if err != nil {
				return err
			}
			rc, err := cli.GetFile(cmd.Context(), collection, path)
			if err != nil {
				return err
			}
			defer func() { _ = rc.Close() }()

			dst := io.Writer(a.out)
			if outFile != "" {
				f, err := os.Create(outFile) //nolint:gosec // user-supplied output path
				if err != nil {
					return err
				}
				defer func() { _ = f.Close() }()
				dst = f
			}
			if _, err := io.Copy(dst, rc); err != nil {
				return err
			}
			if outFile != "" {
				a.printf("wrote %s\n", outFile)
			}
			return nil
		},
	}
	cmd.Flags().StringVarP(&outFile, "output-file", "o", "", "write to file instead of stdout")
	return cmd
}

func (a *app) newRmCmd() *cobra.Command {
	var recursive bool
	cmd := &cobra.Command{
		Use:   "rm <collection>/<path>",
		Short: "Delete a file (or a folder subtree with -r)",
		Args:  exactArgs(1, "<collection>/<path>"),
		RunE: func(cmd *cobra.Command, args []string) error {
			cli, err := a.client()
			if err != nil {
				return err
			}
			collection, path, err := splitTarget(args[0])
			if err != nil {
				return err
			}
			if recursive {
				if err := cli.DeleteFolder(cmd.Context(), collection, path); err != nil {
					return err
				}
				a.printf("deleted %s/%s/ (recursive)\n", collection, path)
				return nil
			}
			if err := cli.DeleteFile(cmd.Context(), collection, path); err != nil {
				if apiclient.IsConflict(err) {
					return fmt.Errorf("%s/%s is a folder; use rm -r to delete it and its contents", collection, path)
				}
				return err
			}
			a.printf("deleted %s/%s\n", collection, path)
			return nil
		},
	}
	cmd.Flags().BoolVarP(&recursive, "recursive", "r", false, "delete a folder and everything beneath it")
	return cmd
}

// defaultLargeUploadThreshold is put-tree's default cutover point (bytes)
// from a direct PUT to a large-upload session (design/10 §3, design/07 §3):
// big enough that most text/HTML/image artifacts go straight through, small
// enough to avoid buffering large bodies through the JSON-API request path.
const defaultLargeUploadThreshold = 8 << 20 // 8 MiB

// putTreeOpts carries put-tree's per-run settings into the walk.
type putTreeOpts struct {
	ctAuto    bool
	include   string
	exclude   string
	threshold int64
	metadata  json.RawMessage
}

func (a *app) newPutTreeCmd() *cobra.Command {
	var (
		contentTypeAuto      bool
		include              string
		exclude              string
		largeUploadThreshold int64
		metaFlags            uploadMetaFlags
	)
	cmd := &cobra.Command{
		Use:   "put-tree <collection> <localdir>",
		Short: "Upload a directory tree",
		Args:  exactArgs(2, "<collection> <localdir>"),
		RunE: func(cmd *cobra.Command, args []string) error {
			cli, err := a.client()
			if err != nil {
				return err
			}
			doc, err := metaFlags.document()
			if err != nil {
				return err
			}
			a.printGeneratedToken(metaFlags.auth.genToken)
			return a.putTree(cmd, cli, args[0], args[1], putTreeOpts{
				ctAuto: contentTypeAuto, include: include, exclude: exclude,
				threshold: largeUploadThreshold, metadata: doc,
			})
		},
	}
	f := cmd.Flags()
	f.BoolVar(&contentTypeAuto, "content-type-auto", false, "infer content type per file from extension")
	f.StringVar(&include, "include", "", "only upload paths matching this glob")
	f.StringVar(&exclude, "exclude", "", "skip paths matching this glob")
	f.Int64Var(&largeUploadThreshold, "large-upload-threshold", defaultLargeUploadThreshold,
		"files at or above this size (bytes) use a large-upload session instead of a direct PUT")
	metaFlags.register(f)
	return cmd
}

// treeFile is one upload candidate discovered by the put-tree walk.
type treeFile struct {
	abs  string
	rel  string
	size int64
}

// putTree walks localDir, applying include/exclude globs (matched against the
// forward-slash relative path), then uploads each match under
// <collection>/<relpath> — a direct PUT for files under threshold, a
// large-upload session (design/07 §3) for files at or above it — and reports
// a summary. A progress bar (design/10 intro) tracks completion on stderr
// when it is a terminal.
func (a *app) putTree(cmd *cobra.Command, cli *apiclient.Client, collection, localDir string, opts putTreeOpts) error {
	files, err := discoverTreeFiles(localDir, opts.include, opts.exclude)
	if err != nil {
		return err
	}

	prog := newUploadProgress(a.err, len(files), a.quiet, a.jsonOutput())
	var uploaded int
	var totalBytes int64
	for _, tf := range files {
		ct := ""
		if opts.ctAuto {
			ct = mime.TypeByExtension(filepath.Ext(tf.abs))
		}
		if err := a.putTreeFile(cmd, cli, collection, tf, ct, opts.threshold, opts.metadata); err != nil {
			prog.finish()
			return err
		}
		uploaded++
		totalBytes += tf.size
		prog.advance(tf.rel)
		if a.verbose {
			a.printf("  %s\n", tf.rel)
		}
	}
	prog.finish()

	if a.jsonOutput() {
		return a.printJSON(map[string]any{"uploaded": uploaded, "bytes": totalBytes})
	}
	a.printf("uploaded %d files (%d bytes) to %s\n", uploaded, totalBytes, collection)
	return nil
}

// putTreeFile uploads one file: a direct PUT below threshold, a large-upload
// session (begin + capability PUT) at or above it. metadata (optional) is
// applied to every uploaded file.
func (a *app) putTreeFile(cmd *cobra.Command, cli *apiclient.Client, collection string, tf treeFile, contentType string, threshold int64, metadata json.RawMessage) error {
	f, err := os.Open(tf.abs) //nolint:gosec // walking a user-supplied directory to upload
	if err != nil {
		return err
	}
	defer func() { _ = f.Close() }()

	if threshold > 0 && tf.size >= threshold {
		session, err := cli.BeginUpload(cmd.Context(), collection, apiclient.BeginUploadRequest{Path: tf.rel, Metadata: metadata})
		if err != nil {
			return fmt.Errorf("beginning upload for %s: %w", tf.rel, err)
		}
		if _, err := cli.PutUploadURL(cmd.Context(), session.UploadURL, f, contentType); err != nil {
			return fmt.Errorf("uploading %s: %w", tf.rel, err)
		}
		return nil
	}

	if _, err := cli.PutFile(cmd.Context(), collection, tf.rel, f, apiclient.PutFileOptions{ContentType: contentType, Metadata: metadata}); err != nil {
		return fmt.Errorf("uploading %s: %w", tf.rel, err)
	}
	return nil
}

// discoverTreeFiles walks localDir and returns every regular file passing the
// include/exclude globs, relative to localDir with forward slashes.
func discoverTreeFiles(localDir, include, exclude string) ([]treeFile, error) {
	var files []treeFile
	err := filepath.Walk(localDir, func(p string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if info.IsDir() {
			return nil
		}
		rel, err := filepath.Rel(localDir, p)
		if err != nil {
			return err
		}
		rel = filepath.ToSlash(rel)
		ok, err := matchGlobs(rel, include, exclude)
		if err != nil {
			return err
		}
		if !ok {
			return nil
		}
		files = append(files, treeFile{abs: p, rel: rel, size: info.Size()})
		return nil
	})
	if err != nil {
		return nil, err
	}
	return files, nil
}

// renderFile prints a file info as JSON or a short text summary.
func (a *app) renderFile(info *apiclient.FileInfo, textMsg string) error {
	if a.jsonOutput() {
		return a.printJSON(info)
	}
	if textMsg != "" {
		a.printf(textMsg, info.Path, info.Size)
	}
	a.printf("url: %s\n", info.URL)
	return nil
}

// splitTarget splits "collection/path" into its two parts.
func splitTarget(arg string) (collection, path string, err error) {
	collection, path, ok := strings.Cut(arg, "/")
	if !ok || collection == "" || path == "" {
		return "", "", usagef("target %q must be <collection>/<path>", arg)
	}
	return collection, path, nil
}

// matchGlobs reports whether rel passes the include/exclude filters.
func matchGlobs(rel, include, exclude string) (bool, error) {
	if include != "" {
		ok, err := filepath.Match(include, rel)
		if err != nil {
			return false, usagef("invalid --include glob: %v", err)
		}
		if !ok {
			return false, nil
		}
	}
	if exclude != "" {
		ok, err := filepath.Match(exclude, rel)
		if err != nil {
			return false, usagef("invalid --exclude glob: %v", err)
		}
		if ok {
			return false, nil
		}
	}
	return true, nil
}
