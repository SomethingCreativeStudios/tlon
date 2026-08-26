package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/SomethingCreativeStudios/tlon/application"
	"github.com/SomethingCreativeStudios/tlon/auth"
	"github.com/SomethingCreativeStudios/tlon/config"
	"github.com/SomethingCreativeStudios/tlon/demo"
	"github.com/SomethingCreativeStudios/tlon/postgres"
	"github.com/SomethingCreativeStudios/tlon/server"
	"github.com/SomethingCreativeStudios/tlon/store"
)

var version = "dev"

func main() {
	if err := run(os.Args[1:]); err != nil {
		slog.Error("tlon failed", "error", err)
		os.Exit(1)
	}
}

func run(args []string) error {
	if len(args) == 0 {
		return usageError()
	}
	switch args[0] {
	case "serve":
		return serve(args[1:])
	case "migrate":
		return migrate(args[1:])
	case "catalog":
		return catalogCommand(args[1:])
	case "demo":
		return demoCommand(args[1:])
	case "healthcheck":
		return healthcheck(args[1:])
	case "version":
		fmt.Println(version)
		return nil
	default:
		return usageError()
	}
}

func usageError() error {
	return fmt.Errorf("usage: tlon <serve|migrate|catalog|demo|healthcheck|version> [arguments]")
}

func serve(args []string) error {
	flags := flag.NewFlagSet("serve", flag.ContinueOnError)
	if err := flags.Parse(args); err != nil {
		return err
	}
	cfg, err := config.Load()
	if err != nil {
		return err
	}
	logger := newLogger(cfg.LogLevel)
	slog.SetDefault(logger)
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()
	database, err := postgres.Open(ctx, cfg.DatabaseURL, postgres.Options{})
	if err != nil {
		return err
	}
	defer database.Close()
	if err := database.Ping(ctx); err != nil {
		return fmt.Errorf("database readiness: %w", err)
	}
	var authorizer auth.Authorizer = auth.DenyMutations{}
	if cfg.AuthorizationMode == "external" {
		authorizer = auth.External{}
	}
	app := application.New(database, authorizer, cfg.PublicURL)
	handler, err := server.New(app, cfg.CursorSecret, server.Options{DefaultLimit: cfg.DefaultLimit, MaximumLimit: cfg.MaximumLimit, Logger: logger})
	if err != nil {
		return err
	}
	httpServer := &http.Server{Addr: cfg.ListenAddress, Handler: handler, ReadHeaderTimeout: cfg.ReadTimeout, ReadTimeout: cfg.ReadTimeout, WriteTimeout: cfg.WriteTimeout, IdleTimeout: cfg.IdleTimeout}
	serverErrors := make(chan error, 1)
	go func() {
		logger.Info("Tlon listening", "address", cfg.ListenAddress, "public_url", cfg.PublicURL, "auth_mode", cfg.AuthorizationMode, "version", version)
		serverErrors <- httpServer.ListenAndServe()
	}()
	select {
	case err := <-serverErrors:
		if !errors.Is(err, http.ErrServerClosed) {
			return err
		}
		return nil
	case <-ctx.Done():
		shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), cfg.ShutdownTimeout)
		defer shutdownCancel()
		return httpServer.Shutdown(shutdownCtx)
	}
}

func migrate(args []string) error {
	if len(args) == 0 {
		return fmt.Errorf("usage: tlon migrate <up|status>")
	}
	databaseURL, rest, err := databaseArgs("migrate", args[1:])
	if err != nil {
		return err
	}
	if len(rest) > 0 {
		return fmt.Errorf("unexpected migrate arguments: %s", strings.Join(rest, " "))
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()
	database, err := postgres.Open(ctx, databaseURL, postgres.Options{})
	if err != nil {
		return err
	}
	defer database.Close()
	switch args[0] {
	case "up":
		return database.Migrate(ctx)
	case "status":
		statuses, err := database.MigrationStatus(ctx)
		if err != nil {
			return err
		}
		for _, status := range statuses {
			state := "pending"
			if status.Applied {
				state = "applied"
			}
			fmt.Printf("%03d %-12s %s\n", status.Version, state, status.Name)
		}
		return nil
	default:
		return fmt.Errorf("usage: tlon migrate <up|status>")
	}
}

func catalogCommand(args []string) error {
	if len(args) == 0 {
		return fmt.Errorf("usage: tlon catalog <apply|get|list|delete>")
	}
	operation := args[0]
	flags := flag.NewFlagSet("catalog "+operation, flag.ContinueOnError)
	databaseURL := flags.String("database-url", os.Getenv("TLON_DATABASE_URL"), "PostgreSQL connection URL")
	cascade := flags.Bool("cascade", false, "delete records in the catalog")
	if err := flags.Parse(args[1:]); err != nil {
		return err
	}
	if *databaseURL == "" {
		return fmt.Errorf("--database-url or TLON_DATABASE_URL is required")
	}
	rest := flags.Args()
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	database, err := postgres.Open(ctx, *databaseURL, postgres.Options{})
	if err != nil {
		return err
	}
	defer database.Close()
	switch operation {
	case "apply":
		if len(rest) != 1 {
			return fmt.Errorf("usage: tlon catalog apply [--database-url URL] FILE")
		}
		body, err := os.ReadFile(rest[0])
		if err != nil {
			return err
		}
		var bundle store.CatalogBundle
		if err := json.Unmarshal(body, &bundle); err != nil {
			return fmt.Errorf("decode catalog bundle: %w", err)
		}
		value, err := database.ApplyCatalog(ctx, bundle)
		if err != nil {
			return err
		}
		fmt.Println(value.ID)
		return nil
	case "get":
		if len(rest) != 1 {
			return fmt.Errorf("usage: tlon catalog get [--database-url URL] ID")
		}
		value, err := database.GetCatalog(ctx, rest[0])
		if err != nil {
			return err
		}
		return writeJSON(value.Bundle)
	case "list":
		if len(rest) != 0 {
			return fmt.Errorf("usage: tlon catalog list [--database-url URL]")
		}
		result, err := database.ListCatalogs(ctx, store.Search{Limit: 10000, Sort: []store.SortField{{Property: "id", Direction: "asc"}}})
		if err != nil {
			return err
		}
		summaries := make([]map[string]any, 0, len(result.Catalogs))
		for _, value := range result.Catalogs {
			var doc map[string]any
			_ = json.Unmarshal(value.Bundle.Catalog, &doc)
			summaries = append(summaries, map[string]any{"id": value.ID, "title": doc["title"]})
		}
		return writeJSON(summaries)
	case "delete":
		if len(rest) != 1 {
			return fmt.Errorf("usage: tlon catalog delete [--database-url URL] [--cascade] ID")
		}
		return database.DeleteCatalog(ctx, rest[0], *cascade)
	default:
		return fmt.Errorf("usage: tlon catalog <apply|get|list|delete>")
	}
}

func demoCommand(args []string) error {
	if len(args) == 0 || args[0] != "seed" {
		return fmt.Errorf("usage: tlon demo seed [--database-url URL] [--catalog ID] [--count N] [--seed N] [--reset]")
	}
	flags := flag.NewFlagSet("demo seed", flag.ContinueOnError)
	databaseURL := flags.String("database-url", os.Getenv("TLON_DATABASE_URL"), "PostgreSQL connection URL")
	publicURL := flags.String("public-url", getenv("TLON_PUBLIC_URL", "http://localhost:8080"), "absolute URL used for materialized record links")
	catalogID := flags.String("catalog", demo.DefaultCatalogID, "demo catalog identifier")
	count := flags.Int("count", demo.DefaultCount, "number of deterministic records")
	seed := flags.Int64("seed", demo.DefaultSeed, "content generation seed")
	reset := flags.Bool("reset", false, "delete and recreate only the selected demo catalog")
	if err := flags.Parse(args[1:]); err != nil {
		return err
	}
	if len(flags.Args()) != 0 {
		return fmt.Errorf("unexpected demo arguments: %s", strings.Join(flags.Args(), " "))
	}
	if *databaseURL == "" {
		return fmt.Errorf("--database-url or TLON_DATABASE_URL is required")
	}
	if *count < 1 || *count > demo.MaximumCount {
		return fmt.Errorf("--count must be between 1 and %d", demo.MaximumCount)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
	defer cancel()
	database, err := postgres.Open(ctx, *databaseURL, postgres.Options{})
	if err != nil {
		return err
	}
	defer database.Close()
	result, err := demo.Seed(ctx, database, demo.SeedOptions{CatalogID: *catalogID, Count: *count, Seed: *seed, Reset: *reset, PublicURL: *publicURL})
	if err != nil {
		return err
	}
	fmt.Printf("seeded catalog %s with %d records (%d created, %d replaced; seed %d)\n", result.CatalogID, result.Created+result.Replaced, result.Created, result.Replaced, result.Seed)
	return nil
}

func healthcheck(args []string) error {
	flags := flag.NewFlagSet("healthcheck", flag.ContinueOnError)
	endpoint := flags.String("url", getenv("TLON_HEALTHCHECK_URL", "http://127.0.0.1:8080/healthz"), "health endpoint")
	if err := flags.Parse(args); err != nil {
		return err
	}
	client := http.Client{Timeout: 3 * time.Second}
	response, err := client.Get(*endpoint)
	if err != nil {
		return err
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return fmt.Errorf("health endpoint returned %s", response.Status)
	}
	return nil
}

func databaseArgs(name string, args []string) (string, []string, error) {
	flags := flag.NewFlagSet(name, flag.ContinueOnError)
	databaseURL := flags.String("database-url", os.Getenv("TLON_DATABASE_URL"), "PostgreSQL connection URL")
	if err := flags.Parse(args); err != nil {
		return "", nil, err
	}
	if *databaseURL == "" {
		return "", nil, fmt.Errorf("--database-url or TLON_DATABASE_URL is required")
	}
	return *databaseURL, flags.Args(), nil
}
func writeJSON(value any) error {
	encoder := json.NewEncoder(os.Stdout)
	encoder.SetIndent("", "  ")
	return encoder.Encode(value)
}
func getenv(name, fallback string) string {
	if value := os.Getenv(name); value != "" {
		return value
	}
	return fallback
}
func newLogger(level string) *slog.Logger {
	var parsed slog.Level
	switch strings.ToLower(level) {
	case "debug":
		parsed = slog.LevelDebug
	case "warn":
		parsed = slog.LevelWarn
	case "error":
		parsed = slog.LevelError
	default:
		parsed = slog.LevelInfo
	}
	return slog.New(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{Level: parsed}))
}
