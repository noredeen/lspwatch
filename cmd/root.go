package cmd

import (
	"fmt"
	"os"

	"github.com/noredeen/lspwatch/internal"

	"github.com/spf13/cobra"
)

// TODO:
// - [ ] units in datadog exporter
// - [ ] make request buffer thread-safe?

var configFilePath string
var logDir string
var mode string

var rootCmd = &cobra.Command{
	Use:   "lspwatch",
	Short: "lspwatch provides observability for LSP-compliant language servers over stdin/stdout",
	Args:  cobra.MinimumNArgs(1),
	Run: func(cmd *cobra.Command, args []string) {
		var serverArgs []string
		if len(args) > 1 {
			serverArgs = args[1:]
		} else {
			serverArgs = []string{}
		}

		lspwatchInstance, err := internal.NewLspwatchInstance(args[0], serverArgs, configFilePath, logDir, mode)

		if err != nil {
			fmt.Printf("error setting up lspwatch: %v\n", err)
			os.Exit(1)
		}

		lspwatchInstance.Run()
	},
}

func Execute() {
	if err := rootCmd.Execute(); err != nil {
		fmt.Println(err)
		os.Exit(1)
	}
}

func init() {
	rootCmd.PersistentFlags().StringVar(&configFilePath, "config", "", "path to config file for lspwatch")
	rootCmd.PersistentFlags().StringVar(&logDir, "logdir", "", "path to log directory for lspwatch")
	rootCmd.PersistentFlags().StringVar(&mode, "mode", "", "lpswatch mode (`command` or `proxy`)")
}
