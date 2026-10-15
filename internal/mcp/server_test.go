package mcp

import (
	"context"
	"log/slog"
	"os"
	"testing"

	"mcp-search-server/internal/search"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

func TestWebSearchToolRegistered(t *testing.T) {
	cfg := search.DefaultSafeSearchConfig()
	cfg.Enabled = false
	searcher := search.New(slog.New(slog.NewTextHandler(os.Stderr, nil)))
	safeSearcher := search.NewSafeSearcher(searcher, cfg)
	server := NewServer(safeSearcher, slog.Default())

	ctx := context.Background()

	// Создаём пару in-memory транспортов: клиент ↔ сервер.
	clientTransport, serverTransport := mcp.NewInMemoryTransports()

	// Подключаем сервер к его транспорту.
	_, err := server.mcpServer.Connect(ctx, serverTransport, nil)
	if err != nil {
		t.Fatalf("server connect: %v", err)
	}

	// Создаём клиента и подключаем его к клиентскому транспорту.
	client := mcp.NewClient(
		&mcp.Implementation{Name: "test-client", Version: "v0.0.1"},
		nil,
	)
	session, err := client.Connect(ctx, clientTransport, nil)
	if err != nil {
		t.Fatalf("client connect: %v", err)
	}
	defer session.Close()

	// Теперь ListTools доступен на клиентской сессии.
	res, err := session.ListTools(ctx, nil)
	if err != nil {
		t.Fatalf("ListTools failed: %v", err)
	}
	if len(res.Tools) != 1 {
		t.Fatalf("expected 1 tool, got %d", len(res.Tools))
	}
	if res.Tools[0].Name != "web_search" {
		t.Errorf("expected tool name web_search, got %s", res.Tools[0].Name)
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