package main

import "github.com/spf13/cobra"

// newMaintenanceCmd builds `artifactctl maintenance cleanup` (design/10 §2).
func (a *app) newMaintenanceCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "maintenance",
		Short: "Trigger maintenance tasks",
	}
	cmd.AddCommand(a.newMaintenanceCleanupCmd())
	return cmd
}

func (a *app) newMaintenanceCleanupCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "cleanup",
		Short: "Trigger an on-demand janitor pass",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			cli, err := a.client()
			if err != nil {
				return err
			}
			result, err := cli.MaintenanceCleanup(cmd.Context())
			if err != nil {
				return err
			}
			if a.jsonOutput() {
				return a.printJSON(result)
			}
			a.printf("cleanup: %s\n", result.Cleanup)
			return nil
		},
	}
}
