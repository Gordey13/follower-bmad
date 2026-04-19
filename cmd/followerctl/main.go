package main

import (
	"context"
	"fmt"
	"os"
	"strings"

	"follower/internal/cli/adminclient"
	"follower/internal/cli/commands"
)

const defaultAdminAPIBaseURL = "http://127.0.0.1:8080"

func main() {
	baseURL := strings.TrimSpace(os.Getenv("FOLLOWERCTL_API_BASE_URL"))
	if baseURL == "" {
		baseURL = defaultAdminAPIBaseURL
	}

	client, err := adminclient.New(baseURL, nil)
	if err != nil {
		_, _ = fmt.Fprintf(os.Stderr, "error [CLI_CONFIG_ERROR]: %s\n", err)
		os.Exit(1)
	}

	code := commands.Execute(
		context.Background(),
		os.Args[1:],
		commands.Dependencies{
			Client: client,
			Stdout: os.Stdout,
			Stderr: os.Stderr,
		},
	)

	os.Exit(code)
}
