package cmd

import (
	"fmt"
	"lspwatch/internal"
	"os"
	"os/exec"
	"os/signal"
	"strings"
	"syscall"

	log "github.com/sirupsen/logrus"
	"github.com/spf13/cobra"
)

// NOTE: This is probably a little broken on Windows
// TODO: Make request buffer thread-safe?

var rootCmd = &cobra.Command {
    Use:   "lspwatch",
    Short: "lspwatch provides observability for LSP-compliant language servers",
    Args: cobra.MinimumNArgs(1),
    Run: func(cmd *cobra.Command, args []string) {
        RunWatcher(strings.Join(args, " "))
    },
}

func createLogger() (*log.Logger, *os.File) {
    logger := log.New()
    file, err := os.OpenFile("lspwatch.log", os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0644)
    if err != nil {
        // TODO: Maybe don't die if log file can't be created
        logger.Fatal("Failed to create log file")
    }

    logger.Out = file

    return logger, file
}

func launchInterruptHandler(serverCmd *exec.Cmd, logger *log.Logger) {
    signalChan := make(chan os.Signal, 1)
    signal.Notify(signalChan, os.Interrupt, syscall.SIGTERM, syscall.SIGINT, syscall.SIGQUIT)

    go func() {
        sig := <-signalChan
        logger.Infof("lspwatch process interrupted, forwarding signal to language server...")
        err := serverCmd.Process.Signal(sig)
        if err != nil {
            logger.Errorf("Failed to forward signal to language server process: %v", err)
            os.Exit(1)
        }

        // TODO: Find a way to get the actual exit code after signal forwarding
        // https://github.com/golang/go/issues/26539

        err = serverCmd.Wait()

        if s, ok := sig.(syscall.Signal); ok {
            os.Exit(128 + int(s))
        }
    }()
}

func RunWatcher(serverShellCommand string) {
    logger, logFile := createLogger()

    logger.Info("Starting lspwatch...")

    requestsHandler := internal.NewRequestsHandler()

    serverCmd := exec.Command(serverShellCommand)

    stdoutPipe, err := serverCmd.StdoutPipe()
    if err != nil {
        logger.Fatalf("Failed to create pipe to server's stdout: %v", err)
    }

    stdinPipe, err := serverCmd.StdinPipe()
    if err != nil {
        logger.Fatalf("Failed to create pipe to server's stdin: %v", err)
    }

    logger.Infof("Launched language server process (PID=%v)", serverCmd.Process.Pid)

    err = serverCmd.Start()
    if err != nil {
        // TODO: Log something and exit properly
        return
    }

    launchInterruptHandler(serverCmd, logger)

    go requestsHandler.ListenServer(stdoutPipe, logger)
    go requestsHandler.ListenClient(stdinPipe, logger)

    exitCode := 0
    err = serverCmd.Wait()
    if err != nil {
        logger.Errorf("Langage server has terminated unexpectedly: %v", err)
        exitCode = serverCmd.ProcessState.ExitCode()
        
    } else {
        logger.Info("Langauge server exited successfully")
    }

    logFile.Close()
    stdoutPipe.Close()
    stdinPipe.Close()

    os.Exit(exitCode)
}

func Execute() {
    if err := rootCmd.Execute(); err != nil {
        fmt.Println(err)
        os.Exit(1)
    }
}
