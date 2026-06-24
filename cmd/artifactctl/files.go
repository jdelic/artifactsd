package main

import (
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
	var contentType string
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
			f, err := os.Open(args[1]) //nolint:gosec // user-supplied local file to upload
			if err != nil {
				return err
			}
			defer func() { _ = f.Close() }()

			ct := contentType
			if ct == "" {
				ct = mime.TypeByExtension(filepath.Ext(args[1]))
			}
			info, err := cli.PutFile(cmd.Context(), collection, path, f, apiclient.PutFileOptions{ContentType: ct})
			if err != nil {
				return err
			}
			return a.renderFile(info, "uploaded %s (%d bytes)\n")
		},
	}
	cmd.Flags().StringVar(&contentType, "content-type", "", "content type (default: inferred from extension)")
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
	return &cobra.Command{
		Use:   "rm <collection>/<path>",
		Short: "Delete a file",
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
			if err := cli.DeleteFile(cmd.Context(), collection, path); err != nil {
				return err
			}
			a.printf("deleted %s/%s\n", collection, path)
			return nil
		},
	}
}

func (a *app) newPutTreeCmd() *cobra.Command {
	var (
		contentTypeAuto bool
		include         string
		exclude         string
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
			return a.putTree(cmd, cli, args[0], args[1], contentTypeAuto, include, exclude)
		},
	}
	f := cmd.Flags()
	f.BoolVar(&contentTypeAuto, "content-type-auto", false, "infer content type per file from extension")
	f.StringVar(&include, "include", "", "only upload paths matching this glob")
	f.StringVar(&exclude, "exclude", "", "skip paths matching this glob")
	return cmd
}

// putTree walks localDir and uploads each file under <collection>/<relpath>,
// applying include/exclude globs (matched against the forward-slash relative
// path) and optional per-file content-type inference, then reports a summary.
//
// Large-upload sessions for big files (design/10 §3) await the uploads epic
// (artifactsd-4v3); every file is uploaded with a direct PUT for now.
func (a *app) putTree(cmd *cobra.Command, cli *apiclient.Client, collection, localDir string, ctAuto bool, include, exclude string) error {
	var uploaded int
	var totalBytes int64
	walkErr := filepath.Walk(localDir, func(p string, info os.FileInfo, err error) error {
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
		f, err := os.Open(p) //nolint:gosec // walking a user-supplied directory to upload
		if err != nil {
			return err
		}
		defer func() { _ = f.Close() }()

		opts := apiclient.PutFileOptions{}
		if ctAuto {
			opts.ContentType = mime.TypeByExtension(filepath.Ext(p))
		}
		if _, err := cli.PutFile(cmd.Context(), collection, rel, f, opts); err != nil {
			return fmt.Errorf("uploading %s: %w", rel, err)
		}
		uploaded++
		totalBytes += info.Size()
		if a.verbose {
			a.printf("  %s\n", rel)
		}
		return nil
	})
	if walkErr != nil {
		return walkErr
	}
	if a.jsonOutput() {
		return a.printJSON(map[string]any{"uploaded": uploaded, "bytes": totalBytes})
	}
	a.printf("uploaded %d files (%d bytes) to %s\n", uploaded, totalBytes, collection)
	return nil
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
