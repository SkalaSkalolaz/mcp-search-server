package mcp

import (
	"context"
	"log/slog"
	"os"
	"testing"

	"mcp-search-server/internal/search"
)

func TestWebSearchToolRegistered(t *testing.T) {
	// Создаём сервер с отключённым поиском.
	cfg := search.DefaultSafeSearchConfig()
	cfg.Enabled = false
	searcher := search.New(slog.New(slog.NewTextHandler(os.Stderr, nil)))
	safeSearcher := search.NewSafeSearcher(searcher, cfg)
	server := NewServer(safeSearcher, slog.Default())

	if server.mcpServer == nil {
		t.Fatal("mcpServer is nil")
	}

	// Проверяем, что инструмент зарегистрирован через ListTools.
	ctx := context.Background()
	tools, err := server.mcpServer.ListTools(ctx)
	if err != nil {
		t.Fatalf("ListTools failed: %v", err)
	}
	if len(tools) != 1 {
		t.Fatalf("expected 1 tool, got %d", len(tools))
	}
	if tools[0].Name != "web_search" {
		t.Errorf("expected tool name web_search, got %s", tools[0].Name)
	}
}

func TestWebSearchDisabled(t *testing.T) {
	cfg := search.DefaultSafeSearchConfig()
	cfg.Enabled = false
	searcher := search.New(slog.New(slog.NewTextHandler(os.Stderr, nil)))
	safeSearcher := search.NewSafeSearcher(searcher, cfg)
	server := NewServer(safeSearcher, slog.Default())

	ctx := context.Background()
	result, _, err := server.handleWebSearch(ctx, nil, SearchArgs{Query: "test"})
	if err != nil {
		t.Fatalf("unexpected protocol error: %v", err)
	}
	if result == nil || !result.IsError {
		t.Fatal("expected IsError=true when search disabled")
	}
}