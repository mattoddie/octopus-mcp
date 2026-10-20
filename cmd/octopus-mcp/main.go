// Command octopus-mcp is a Model Context Protocol server for Octopus Energy.
//
//	octopus-mcp              serve MCP (stdio, or Streamable HTTP with MCP_TRANSPORT=http)
//	octopus-mcp status       check the API key and list the accounts it can see
//	octopus-mcp healthcheck  check the HTTP server is up (for Docker HEALTHCHECK)
//	octopus-mcp version      print the version
package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/mattoddie/octopus-mcp/internal/config"
	"github.com/mattoddie/octopus-mcp/internal/httpserver"
	"github.com/mattoddie/octopus-mcp/internal/octopus"
	"github.com/mattoddie/octopus-mcp/internal/tools"
)

func main() {
	// Logs go to stderr: stdout is the MCP channel in stdio mode.
	log.SetFlags(0)
	log.SetOutput(os.Stderr)
	cmd := "serve"
	if len(os.Args) > 1 {
		cmd = os.Args[1]
	}
	var err error
	switch cmd {
	case "serve":
		err = serve()
	case "status":
		err = status()
	case "healthcheck":
		err = healthcheck()
	case "version", "--version", "-v":
		fmt.Println(tools.Version)
	case "help", "--help", "-h":
		fmt.Print(usage)
	default:
		err = fmt.Errorf("unknown command %q\n\n%s", cmd, usage)
	}
	if err != nil {
		log.Printf("octopus-mcp: %v", err)
		os.Exit(1)
	}
}

const usage = `Usage: octopus-mcp [command]

Commands:
  serve        Serve MCP (default). stdio, or Streamable HTTP with MCP_TRANSPORT=http
  status       Check OCTOPUS_API_KEY works and list the accounts it can see
  healthcheck  Exit 0 if the HTTP server answers /health (for Docker HEALTHCHECK)
  version      Print the version

Configuration is by environment variable; see https://octopus-mcp.mattoddie.dev/docs/configuration/
`

func load() (*config.Config, *octopus.Client, error) {
	cfg, err := config.Load(os.LookupEnv)
	if err != nil {
		return nil, nil, err
	}
	return cfg, octopus.New(cfg.Octopus), nil
}

func serve() error {
	cfg, client, err := load()
	if err != nil {
		return err
	}
	writes := "disabled"
	if cfg.Tools.AllowWrites {
		writes = "ENABLED"
	}
	account := cfg.Octopus.Account
	if account == "" {
		account = "discovered from the API key"
	}
	log.Printf("octopus-mcp %s: %s, account %s, writes %s, toolsets %d, transport %s", tools.Version, cfg.Octopus.APIURL, account, writes, len(cfg.Tools.Toolsets), cfg.Transport)

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()
	server := tools.NewServer(client, cfg.Tools, nil)
	if cfg.Transport == "stdio" {
		err := server.Run(ctx, &mcp.StdioTransport{})
		if errors.Is(err, context.Canceled) {
			return nil
		}
		return err
	}
	// No WriteTimeout: a tool call that pages through months of readings can take a while.
	srv := &http.Server{
		Addr:              httpserver.Addr(cfg.HTTP),
		Handler:           httpserver.Handler(cfg.HTTP, server),
		ReadHeaderTimeout: 10 * time.Second,
		ReadTimeout:       60 * time.Second,
		IdleTimeout:       120 * time.Second,
	}
	go func() {
		<-ctx.Done()
		shutdown, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = srv.Shutdown(shutdown)
	}()
	return httpserver.Serve(srv, cfg.HTTP)
}

// status checks the API key by getting a Kraken token and listing the accounts it can see.
func status() error {
	cfg, client, err := load()
	if err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	accounts, err := client.Accounts(ctx)
	if err != nil {
		return err
	}
	out := map[string]any{"api_key": "accepted", "accounts": accounts, "graphql": cfg.Octopus.GraphQLURL, "rest": cfg.Octopus.APIURL + "/v1"}
	if cfg.Octopus.Account != "" {
		out["default_account"] = cfg.Octopus.Account
	}
	b, _ := json.MarshalIndent(out, "", "  ")
	fmt.Println(string(b))
	return nil
}

func healthcheck() error {
	url, err := config.HealthcheckURL(os.LookupEnv)
	if err != nil || url == "" {
		return err
	}
	c := &http.Client{Timeout: 3 * time.Second}
	resp, err := c.Get(url)
	if err != nil {
		return err
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("health check returned %s", resp.Status)
	}
	return nil
}
