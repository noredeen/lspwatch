package cmd

import (
	"fmt"
	"os"

	"github.com/noredeen/lspwatch/internal"

	"github.com/spf13/cobra"
)

// TODO:
// - [x] support -c or --config-file
// - [x] finish datadog exporter
// - [x] finish otel file exporter
// - [ ] tags
// - [ ] units in datadog exporter
// - [ ] define in config file which methods to record

// - [ ] report resource consumption for server process
// - [ ] make request buffer thread-safe?
// - [ ] better log file management

var cfgFilePath string
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

		lspwatchInstance, err := internal.NewLspwatchInstance(args[0], serverArgs, cfgFilePath)

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
	rootCmd.PersistentFlags().StringVar(&cfgFilePath, "config", "", "path to config file for lspwatch")
}
