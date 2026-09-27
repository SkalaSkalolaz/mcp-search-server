package main

import (
	"context"
	"flag"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"mcp-search-server/internal/mcp"
	"mcp-search-server/internal/search"
)

// Запуск MCP-сервера
// 
// С включённым поиском
// 
// SEARCH_ENABLED=true ./mcp-search-server --addr :8090
// 
// С кастомным whitelist
// 
// SEARCH_ENABLED=true \
// SEARCH_ALLOWED_DOMAINS="pkg.go.dev,github.com,stackoverflow.com" \
// ./mcp-search-server --addr :8090
// 

func main() {
	// ── Флаги командной строки ──────────────────────────────────
	addr := flag.String("addr", envOr("MCP_LISTEN_ADDR", ":8090"),
		"HTTP listen address for MCP server")
	flag.Parse()

	// ── Логгер ──────────────────────────────────────────────────
	logLevel := slog.LevelInfo
	if os.Getenv("MCP_DEBUG") == "true" {
		logLevel = slog.LevelDebug
	}
	log := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{
		Level: logLevel,
	}))

	// ── Конфигурация безопасного поиска ─────────────────────────
	cfg := search.DefaultSafeSearchConfig()
	cfg.Enabled = envBool("SEARCH_ENABLED", false)
	cfg.MaxSearchesPerSession = envInt("SEARCH_MAX_PER_SESSION", cfg.MaxSearchesPerSession)
	cfg.MaxSourcesPerSearch = envInt("SEARCH_MAX_SOURCES", cfg.MaxSourcesPerSearch)
	cfg.MaxTotalContent = envInt("SEARCH_MAX_TOTAL_CONTENT", cfg.MaxTotalContent)
	cfg.LogDir = envOr("SEARCH_LOG_DIR", cfg.LogDir)

	if domains := os.Getenv("SEARCH_ALLOWED_DOMAINS"); domains != "" {
		cfg.AllowedDomains = strings.Split(domains, ",")
	}

	if interval := os.Getenv("SEARCH_MIN_INTERVAL"); interval != "" {
		if d, err := time.ParseDuration(interval); err == nil {
			cfg.MinIntervalBetweenSearches = d
		}
	}

	// ── Создаём Searcher и SafeSearcher ─────────────────────────
	searcher := search.New(log)
	safeSearcher := search.NewSafeSearcher(searcher, cfg)

	// ── Создаём MCP-сервер ──────────────────────────────────────
	mcpServer := mcp.NewServer(safeSearcher, log)

	// ── HTTP mux ────────────────────────────────────────────────
	mux := http.NewServeMux()
	mux.Handle("/mcp", mcpServer.HTTPHandler())

	// Health-check endpoint для мониторинга.
	mux.HandleFunc("/health", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprintf(w, `{"status":"ok","search_enabled":%v}`, cfg.Enabled)
	})

	// ── HTTP-сервер ─────────────────────────────────────────────
	httpServer := &http.Server{
		Addr:         *addr,
		Handler:      mux,
		ReadTimeout:  30 * time.Second,
		WriteTimeout: 120 * time.Second, // поиск может занимать время
		IdleTimeout:  60 * time.Second,
	}

	// ── Graceful shutdown ───────────────────────────────────────
	ctx, stop := signal.NotifyContext(context.Background(),
		os.Interrupt, syscall.SIGTERM)
	defer stop()

	go func() {
		log.Info("MCP search server starting",
			"addr", *addr,
			"search_enabled", cfg.Enabled,
			"allowed_domains", len(cfg.AllowedDomains),
		)
		if err := httpServer.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			log.Error("HTTP server failed", "err", err)
			os.Exit(1)
		}
	}()

	<-ctx.Done()
	log.Info("shutting down...")

	shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := httpServer.Shutdown(shutdownCtx); err != nil {
		log.Error("shutdown failed", "err", err)
	}
	log.Info("server stopped")
}

// ─── Хелперы для env ────────────────────────────────────────────

func envOr(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}

func envBool(key string, fallback bool) bool {
	v := os.Getenv(key)
	if v == "" {
		return fallback
	}
	return v == "true" || v == "1" || v == "yes"
}

func envInt(key string, fallback int) int {
	v := os.Getenv(key)
	if v == "" {
		return fallback
	}
	var n int
	if _, err := fmt.Sscanf(v, "%d", &n); err != nil {
		return fallback
	}
	return n
}