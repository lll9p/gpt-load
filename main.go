// Package main provides the entry point for the GPT-Load proxy server
package main

import (
	"context"
	"embed"
	"fmt"
	"os"
	"os/signal"
	"syscall"

	"gpt-load/internal/app"
	"gpt-load/internal/commands"

	"github.com/sirupsen/logrus"
)

//go:embed web/dist
var buildFS embed.FS

//go:embed web/dist/index.html
var indexPage []byte

func main() {
	if len(os.Args) > 1 {
		runCommand()
	} else {
		runServer()
	}
}

// runCommand dispatches to the appropriate command handler
func runCommand() {
	command := os.Args[1]
	args := os.Args[2:]

	switch command {
	case "migrate-keys":
		commands.RunMigrateKeys(args)
	case "help", "-h", "--help":
		printHelp()
	default:
		fmt.Printf("Unknown command: %s\n", command)
		fmt.Println("Run 'gpt-load help' for usage.")
		os.Exit(1)
	}
}

// printHelp displays the general help information
func printHelp() {
	fmt.Println("GPT-Load - Multi-channel AI proxy with intelligent key rotation.")
	fmt.Println()
	fmt.Println("Usage:")
	fmt.Println("  gpt-load                    Start the proxy server")
	fmt.Println("  gpt-load <command> [args]   Execute a command")
	fmt.Println()
	fmt.Println("Available Commands:")
	fmt.Println("  migrate-keys    Migrate encryption keys")
	fmt.Println("  help            Display this help message")
	fmt.Println()
	fmt.Println("Use 'gpt-load <command> --help' for more information about a command.")
}

// runServer constructs the application and manages its lifecycle.
func runServer() {
	application, err := app.NewApp(buildFS, indexPage)
	if err != nil {
		logrus.Fatalf("Failed to initialize application: %v", err)
	}
	if err := application.Start(); err != nil {
		logrus.Fatalf("Failed to start application: %v", err)
	}
	quit := make(chan os.Signal, 1)
	signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)
	defer signal.Stop(quit)
	<-quit

	ctx, cancel := context.WithTimeout(context.Background(), application.GracefulShutdownTimeout())
	defer cancel()
	if err := application.Stop(ctx); err != nil {
		logrus.Errorf("Application shutdown failed: %v", err)
		os.Exit(1)
	}
}
