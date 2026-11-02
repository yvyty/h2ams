package cli

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"github.com/spf13/cobra"

	"github.com/yvyty/h2ams/internal/config"
)

func newInitCmd() *cobra.Command {
	var force bool
	cmd := &cobra.Command{
		Use:   "init",
		Short: "Create a template " + config.DefaultFileName + " in the current directory",
		Long: "Create a template " + config.DefaultFileName + " in the current directory.\n\n" +
			"If the file already exists, init refuses to overwrite it unless --force is given.",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			dir, err := os.Getwd()
			if err != nil {
				return err
			}
			path, err := writeTemplate(dir, force)
			if err != nil {
				return err
			}
			fmt.Fprintf(cmd.OutOrStdout(), "Created %s\n", path)
			return nil
		},
	}
	cmd.Flags().BoolVarP(&force, "force", "f", false, "overwrite an existing config file")
	return cmd
}

// writeTemplate writes the template config into dir and returns its path.
func writeTemplate(dir string, force bool) (string, error) {
	path := filepath.Join(dir, config.DefaultFileName)
	flags := os.O_WRONLY | os.O_CREATE | os.O_TRUNC
	if !force {
		flags |= os.O_EXCL
	}
	f, err := os.OpenFile(path, flags, 0o644)
	if errors.Is(err, os.ErrExist) {
		return "", fmt.Errorf("%s already exists; use --force to overwrite", path)
	}
	if err != nil {
		return "", err
	}
	if _, err := f.WriteString(configTemplate); err != nil {
		f.Close()
		return "", err
	}
	return path, f.Close()
}
