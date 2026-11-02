package cli

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"github.com/spf13/cobra"

	"github.com/yvyty/h2ams/internal/config"
)

func newInitCmd(opts *rootOptions) *cobra.Command {
	var force bool
	cmd := &cobra.Command{
		Use:   "init",
		Short: "Create a template config file",
		Long: "Create a commented template config (" + config.DefaultFileName + " by default, or the\n" +
			"path given with --config) with two example apps to edit.\n\n" +
			"If the file already exists, init refuses to overwrite it unless --force is given.",
		Example: "  h2ams init\n" +
			"  h2ams init --config dev.yml\n" +
			"  h2ams init --force",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			path, err := filepath.Abs(opts.configPath)
			if err != nil {
				return err
			}
			if err := writeTemplate(path, force); err != nil {
				return err
			}
			fmt.Fprintf(cmd.OutOrStdout(), "Created %s\nEdit it to describe your apps, then run \"h2ams up\".\n", path)
			return nil
		},
	}
	cmd.Flags().BoolVarP(&force, "force", "f", false, "overwrite an existing config file")
	return cmd
}

// writeTemplate writes the template config to path.
func writeTemplate(path string, force bool) error {
	flags := os.O_WRONLY | os.O_CREATE | os.O_TRUNC
	if !force {
		flags |= os.O_EXCL
	}
	f, err := os.OpenFile(path, flags, 0o644)
	if errors.Is(err, os.ErrExist) {
		return fmt.Errorf("%s already exists; use --force to overwrite", path)
	}
	if err != nil {
		return err
	}
	if _, err := f.WriteString(configTemplate); err != nil {
		f.Close()
		return err
	}
	return f.Close()
}
