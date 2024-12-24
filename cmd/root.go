package cmd

import "github.com/spf13/cobra"
import "fmt"
import "os"

// Take LSP command as input
// Spawn process with that command
// Listen for stdin, pass IO between langserv and stdout (to editor)

var rootCmd = &cobra.Command {
  Use:   "lswatch",
  Short: "lswatch provides observability for LSP-compliant language servers",
  Run: func(cmd *cobra.Command, args []string) {
    fmt.Println("HELLO")
  },
}

func Execute() {
  if err := rootCmd.Execute(); err != nil {
    fmt.Println(err)
    os.Exit(1)
  }
}
