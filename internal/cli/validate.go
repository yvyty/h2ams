package cli

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"maps"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/spf13/cobra"

	"github.com/yvyty/h2ams/internal/config"
)

// errInvalid is returned after --json output so the exit status is non-zero
// without repeating the problems, which are already in the JSON.
var errInvalid = errors.New("config is invalid")

// validateResult is the --json output of "h2ams validate".
type validateResult struct {
	Valid    bool     `json:"valid"`
	Config   string   `json:"config"`
	Problems []string `json:"problems"`
}

func newValidateCmd(opts *rootOptions) *cobra.Command {
	var envName string
	var asJSON bool
	cmd := &cobra.Command{
		Use:   "validate",
		Short: "Check the config file without starting anything",
		Long: "Check the config file with the same rules as \"h2ams up\", and also check that\n" +
			"every app's working directory (cwd) exists. Every problem is listed at once.\n" +
			"Nothing is started and the daemon is not contacted.\n\n" +
			"With --env, also check that the environment exists. With --json, print\n" +
			"{\"valid\", \"config\", \"problems\"} for scripts and editors. The exit status is\n" +
			"non-zero when the config is invalid.",
		Example: "  h2ams validate\n" +
			"  h2ams validate --config dev.yml --env testing\n" +
			"  h2ams validate --json",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			path, err := filepath.Abs(opts.configPath)
			if err != nil {
				return err
			}
			res := validateFile(path, envName)
			if asJSON {
				return writeValidateJSON(cmd.OutOrStdout(), res)
			}
			if !res.Valid {
				return fmt.Errorf("%s: invalid config:\n  - %s", path, strings.Join(res.Problems, "\n  - "))
			}
			fmt.Fprintf(cmd.OutOrStdout(), "%s is valid\n", path)
			return nil
		},
	}
	cmd.Flags().StringVarP(&envName, "env", "e", "", "also check that this environment exists")
	cmd.Flags().BoolVar(&asJSON, "json", false, "print the result as JSON")
	return cmd
}

// validateFile loads the config at path and checks what loading cannot:
// the environment name and each app's working directory.
func validateFile(path, envName string) validateResult {
	res := validateResult{Config: path, Problems: []string{}}
	cfg, err := config.Load(path)
	if err != nil {
		var verr *config.ValidationError
		if errors.As(err, &verr) {
			res.Problems = append(res.Problems, verr.Problems...)
		} else {
			res.Problems = append(res.Problems, strings.TrimPrefix(err.Error(), path+": "))
		}
		return res
	}
	if envName != "" {
		if _, ok := cfg.Environments[envName]; !ok {
			res.Problems = append(res.Problems, fmt.Sprintf("--env: unknown environment %q", envName))
		}
	}
	baseDir := filepath.Dir(path)
	for _, name := range slices.Sorted(maps.Keys(cfg.Apps)) {
		dir := cfg.Apps[name].Dir(baseDir)
		info, err := os.Stat(dir)
		switch {
		case errors.Is(err, os.ErrNotExist):
			res.Problems = append(res.Problems, fmt.Sprintf("apps.%s.cwd: %s does not exist", name, dir))
		case err != nil:
			res.Problems = append(res.Problems, fmt.Sprintf("apps.%s.cwd: %v", name, err))
		case !info.IsDir():
			res.Problems = append(res.Problems, fmt.Sprintf("apps.%s.cwd: %s is not a directory", name, dir))
		}
	}
	res.Valid = len(res.Problems) == 0
	return res
}

func writeValidateJSON(w io.Writer, res validateResult) error {
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	if err := enc.Encode(res); err != nil {
		return err
	}
	if !res.Valid {
		return errInvalid
	}
	return nil
}
