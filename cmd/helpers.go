package cmd

import (
	"bufio"
	"fmt"
	"strings"

	"github.com/praaatik/databasemanager/internal/database"
	"github.com/spf13/cobra"
)

// resolveEngine returns the engine for an app resolved from Infisical: the
// stored DB_TYPE is authoritative; typeFlag is only a fallback for legacy
// apps provisioned before DB_TYPE existed, and disagreement between the two
// is an error (it protects against operating on the wrong engine).
func resolveEngine(creds database.Credentials, typeFlag, appName string) (string, error) {
	engine := creds.Type
	if engine == "" {
		if typeFlag == "" {
			return "", fmt.Errorf("cannot determine database type for app %q: DB_TYPE secret is missing; pass --type", appName)
		}
		return typeFlag, nil
	}
	if typeFlag != "" && typeFlag != engine {
		return "", fmt.Errorf("DB_TYPE mismatch: stored type is '%s' but --type flag specifies '%s'", engine, typeFlag)
	}
	return engine, nil
}

// confirmAction writes the prompt to stderr (stdout stays data-only) and
// reads one line from stdin. Only an explicit y/yes proceeds; anything else —
// including EOF — declines.
func confirmAction(cmd *cobra.Command, prompt string) bool {
	fmt.Fprint(cmd.ErrOrStderr(), prompt)
	line, _ := bufio.NewReader(cmd.InOrStdin()).ReadString('\n')
	answer := strings.ToLower(strings.TrimSpace(line))
	return answer == "y" || answer == "yes"
}
