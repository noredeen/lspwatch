package cmd

import (
	"fmt"
	"lspwatch/internal"
	"os"
	"os/exec"

	log "github.com/sirupsen/logrus"
	"github.com/spf13/cobra"
)

// Take LSP command as input
// Spawn process with that command
// Listen for stdin, pass IO between langserv and stdout (to editor)

var serverLaunchCommand string

var rootCmd = &cobra.Command {
    Use:   "lspwatch",
    Short: "lspwatch provides observability for LSP-compliant language servers",
    Run: func(cmd *cobra.Command, args []string) {
        RunWatcher()
    },
}

func RunWatcher() {
    var logger = log.New()
    file, err := os.OpenFile("lspwatch.log", os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0666)
    if err != nil {
        // TODO: Maybe don't die if log file can't be created
        logger.Fatal("Failed to create log file")
    }

    logger.Out = file

    requestsHandler := internal.NewRequestsHandler()

    lsCmd := exec.Command(serverLaunchCommand)
    err = lsCmd.Start()
    if err != nil {
        // TODO: Log something and exit properly
        return
    }

    stdoutPipe, err := lsCmd.StdoutPipe()
    if err != nil {
        logger.Fatalf("Failed to create pipe to server's stdout")
    }

    stdinPipe, err := lsCmd.StdinPipe()
    if err != nil {
        logger.Fatalf("Failed to create pipe to server's stdin")
    }

    go requestsHandler.ListenServer(stdoutPipe, logger)
    go requestsHandler.ListenClient(stdinPipe, logger)
}

func init() {
    // TODO: Make this better (use -- instead)
    rootCmd.Flags().StringVarP(
        &serverLaunchCommand,
        "command",
        "c",
        "",
        "Command to launch the language server (required)",
    )
    rootCmd.MarkFlagRequired("command")
}

func Execute() {
    if err := rootCmd.Execute(); err != nil {
        fmt.Println(err)
        os.Exit(1)
    }
}
