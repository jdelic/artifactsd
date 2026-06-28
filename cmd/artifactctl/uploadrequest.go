package main

import (
	"fmt"

	"github.com/spf13/cobra"

	"github.com/jdelic/artifactsd/pkg/apiclient"
)

// newUploadRequestCmd builds `artifactctl upload-request ...` (design/15 §6).
func (a *app) newUploadRequestCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:     "upload-request",
		Aliases: []string{"ur"},
		Short:   "Create and manage upload request links",
	}
	cmd.AddCommand(
		a.newUploadRequestCreateCmd(),
		a.newUploadRequestListCmd(),
		a.newUploadRequestGetCmd(),
		a.newUploadRequestCloseCmd(),
		a.newUploadRequestRevokeCmd(),
		a.newUploadRequestSubmissionsCmd(),
	)
	return cmd
}

func (a *app) newUploadRequestCreateCmd() *cobra.Command {
	var (
		targetFolder         string
		label                string
		description          string
		allowUploaderAccess  bool
		singleUse            bool
		useSubmissionFolders bool
		maxFiles             int64
		maxFileSize          string
		expires              string
		expiresIn            string
	)
	cmd := &cobra.Command{
		Use:   "create <collection>",
		Short: "Create an upload request link for a collection",
		Args:  exactArgs(1, "<collection>"),
		RunE: func(cmd *cobra.Command, args []string) error {
			cli, err := a.client()
			if err != nil {
				return err
			}

			req := apiclient.CreateUploadRequestRequest{
				Collection:           args[0],
				TargetFolder:         targetFolder,
				AllowUploaderAccess:  allowUploaderAccess,
				SingleUse:            singleUse,
				UseSubmissionFolders: useSubmissionFolders,
			}
			if label != "" {
				req.Label = &label
			}
			if description != "" {
				req.Description = &description
			}
			if maxFiles > 0 {
				req.MaxFiles = &maxFiles
			}
			if maxFileSize != "" {
				sz, err := parseByteSize(maxFileSize)
				if err != nil {
					return usagef("invalid --max-file-size %q: %v", maxFileSize, err)
				}
				req.MaxFileSize = &sz
			}
			expiresAt, err := expiresAtFrom(expires, expiresIn)
			if err != nil {
				return err
			}
			if expiresAt != "" {
				req.ExpiresAt = &expiresAt
			}

			ur, err := cli.CreateUploadRequest(cmd.Context(), req)
			if err != nil {
				return err
			}
			return a.renderUploadRequest(ur, "created upload request %s\n")
		},
	}
	f := cmd.Flags()
	f.StringVar(&targetFolder, "target-folder", "", "destination folder path inside the collection")
	f.StringVar(&label, "label", "", "human-readable label for the link")
	f.StringVar(&description, "description", "", "description shown on the upload form")
	f.BoolVar(&allowUploaderAccess, "allow-uploader-access", false, "show file URLs to uploaders (public/unlisted files only)")
	f.BoolVar(&singleUse, "single-use", false, "auto-close the link after the first successful submission")
	f.BoolVar(&useSubmissionFolders, "use-submission-folders", false, "store each submission in a unique subfolder")
	f.Int64Var(&maxFiles, "max-files", 0, "maximum total files that may be uploaded (0 = unlimited)")
	f.StringVar(&maxFileSize, "max-file-size", "", "per-file size limit, e.g. 10MB, 500KB")
	f.StringVar(&expires, "expires", "", "expire at absolute RFC3339 timestamp")
	f.StringVar(&expiresIn, "expires-in", "", "expire after duration (e.g. 30d, 48h, 2w)")
	return cmd
}

func (a *app) newUploadRequestListCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "list",
		Short: "List all upload request links",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			cli, err := a.client()
			if err != nil {
				return err
			}
			page, err := cli.ListUploadRequests(cmd.Context())
			if err != nil {
				return err
			}
			if a.jsonOutput() {
				return a.printJSON(page)
			}
			for i := range page.Items {
				ur := &page.Items[i]
				a.printf("%s\t%s\t%s\t%s\n", ur.RequestID, ur.Collection, ur.Status, ur.URL)
			}
			return nil
		},
	}
}

func (a *app) newUploadRequestGetCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "get <request-id>",
		Short: "Show an upload request link",
		Args:  exactArgs(1, "<request-id>"),
		RunE: func(cmd *cobra.Command, args []string) error {
			cli, err := a.client()
			if err != nil {
				return err
			}
			ur, err := cli.GetUploadRequest(cmd.Context(), args[0])
			if err != nil {
				return err
			}
			return a.renderUploadRequest(ur, "")
		},
	}
}

func (a *app) newUploadRequestCloseCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "close <request-id>",
		Short: "Close an upload request link (stops accepting new uploads)",
		Args:  exactArgs(1, "<request-id>"),
		RunE: func(cmd *cobra.Command, args []string) error {
			cli, err := a.client()
			if err != nil {
				return err
			}
			status := "closed"
			_, err = cli.PatchUploadRequest(cmd.Context(), args[0], apiclient.PatchUploadRequestRequest{
				Status: &status,
			})
			if err != nil {
				return err
			}
			a.printf("closed upload request %s\n", args[0])
			return nil
		},
	}
}

func (a *app) newUploadRequestRevokeCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "revoke <request-id>",
		Short: "Revoke an upload request link permanently",
		Args:  exactArgs(1, "<request-id>"),
		RunE: func(cmd *cobra.Command, args []string) error {
			cli, err := a.client()
			if err != nil {
				return err
			}
			if err := cli.RevokeUploadRequest(cmd.Context(), args[0]); err != nil {
				return err
			}
			a.printf("revoked upload request %s\n", args[0])
			return nil
		},
	}
}

func (a *app) newUploadRequestSubmissionsCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "submissions <request-id>",
		Short: "List submissions for an upload request link",
		Args:  exactArgs(1, "<request-id>"),
		RunE: func(cmd *cobra.Command, args []string) error {
			cli, err := a.client()
			if err != nil {
				return err
			}
			page, err := cli.ListUploadRequestSubmissions(cmd.Context(), args[0])
			if err != nil {
				return err
			}
			if a.jsonOutput() {
				return a.printJSON(page)
			}
			if len(page.Items) == 0 {
				a.printf("no submissions yet\n")
				return nil
			}
			for i := range page.Items {
				sub := &page.Items[i]
				ip := ""
				if sub.ClientIP != nil {
					ip = *sub.ClientIP
				}
				a.printf("%s\tfiles:%d\tbytes:%d\t%s\t%s\n",
					sub.SubmissionID, sub.FileCount, sub.TotalBytes, ip, sub.CreatedAt)
			}
			return nil
		},
	}
}

// renderUploadRequest prints an upload request as JSON or a short text summary.
func (a *app) renderUploadRequest(ur *apiclient.UploadRequest, textMsg string) error {
	if a.jsonOutput() {
		return a.printJSON(ur)
	}
	if textMsg != "" {
		a.printf(textMsg, ur.RequestID)
	}
	a.printf("id:         %s\n", ur.RequestID)
	a.printf("url:        %s\n", ur.URL)
	a.printf("collection: %s\n", ur.Collection)
	a.printf("status:     %s\n", ur.Status)
	if ur.TargetFolder != "" {
		a.printf("folder:     %s\n", ur.TargetFolder)
	}
	if ur.Label != nil {
		a.printf("label:      %s\n", *ur.Label)
	}
	a.printf("single_use: %v\n", ur.SingleUse)
	a.printf("uploaded:   %d files\n", ur.FilesUploaded)
	if ur.ExpiresAt != nil {
		a.printf("expires:    %s\n", *ur.ExpiresAt)
	}
	return nil
}

// parseByteSize parses a human-readable byte size string, e.g. "10MB", "500KB".
func parseByteSize(s string) (int64, error) {
	if s == "" {
		return 0, fmt.Errorf("empty size string")
	}
	var n float64
	var unit string
	if _, err := fmt.Sscanf(s, "%f%s", &n, &unit); err != nil {
		// Maybe it's just a bare number.
		if _, err2 := fmt.Sscanf(s, "%f", &n); err2 != nil {
			return 0, fmt.Errorf("cannot parse %q", s)
		}
		return int64(n), nil
	}
	mult := int64(1)
	switch unit {
	case "KB", "kb", "K", "k":
		mult = 1024
	case "MB", "mb", "M", "m":
		mult = 1024 * 1024
	case "GB", "gb", "G", "g":
		mult = 1024 * 1024 * 1024
	case "TB", "tb", "T", "t":
		mult = 1024 * 1024 * 1024 * 1024
	case "B", "b", "":
		mult = 1
	default:
		return 0, fmt.Errorf("unknown unit %q (use KB, MB, GB, TB)", unit)
	}
	return int64(n * float64(mult)), nil
}
