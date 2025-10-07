package cmd

import (
	"fmt"

	"github.com/praaatik/databasemanager/internal/config"
	"github.com/spf13/cobra"
)

// getCmd represents the get command
var getCmd = &cobra.Command{
	Use:   "get",
	Short: "get will get one secret based on the <key>",
	Long: `A longer description that spans multiple lines and likely contains examples
and usage of using your command. For example:

Cobra is a CLI library for Go that empowers applications.
This application is a tool to generate the needed files
to quickly create a Cobra application.`,
	Run: func(cmd *cobra.Command, args []string) {
		cfg := cmd.Context().Value(configKey{}).(*config.Config)
		get(cfg)

		fmt.Println("get called")
	},
}

func init() {
	rootCmd.AddCommand(getCmd)
}

func get(cfg *config.Config) {
	fmt.Println("get called ===")
	fmt.Println(cfg.DatabasePort)
	fmt.Println("get done ===")
}
