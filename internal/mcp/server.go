package mcp

import (
	"context"
	"fmt"
	"log/slog"
	"net/http"

	"mcp-search-server/internal/search"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// ─── Аргументы инструмента ──────────────────────────────────────

// SearchArgs — входные параметры инструмента web_search.
//
// Тег jsonschema содержит ТОЛЬКО текст описания.
// Обязательность поля определяется отсутствием omitempty в json-теге.
type SearchArgs struct {
	Query string `json:"query" jsonschema:"Search query to find information on the internet. Be specific and include programming language or library name."`
}

// ─── Результат инструмента ──────────────────────────────────────

// SearchOutput — структурированный результат поиска.
type SearchOutput struct {
	Content string   `json:"content"`
	Sources []string `json:"sources"`
}

// ─── MCP-сервер ─────────────────────────────────────────────────

// Server оборачивает SafeSearcher в MCP-протокол.
type Server struct {
	safeSearcher *search.SafeSearcher
	log          *slog.Logger
	mcpServer    *mcp.Server
}

// NewServer создаёт MCP-сервер с инструментом web_search.
func NewServer(safeSearcher *search.SafeSearcher, log *slog.Logger) *Server {
	s := &Server{
		safeSearcher: safeSearcher,
		log:          log,
	}

	// Создаём MCP-сервер.
	s.mcpServer = mcp.NewServer(
		&mcp.Implementation{
			Name:    "gogitor-search",
			Version: "1.0.0",
		},
		&mcp.ServerOptions{
			Instructions: "Provides safe web search capability. " +
				"Use the web_search tool to find documentation, API references, " +
				"and programming examples. Results are filtered and sanitized.",
		},
	)

	// Регистрируем инструмент с типизированным handler'ом.
	// SDK автоматически сгенерирует JSON Schema из структуры SearchArgs.
	mcp.AddTool(s.mcpServer, &mcp.Tool{
		Name: "web_search",
		Description: "Search the internet for programming documentation, " +
			"API references, error messages, and code examples. " +
			"Only whitelisted domains (pkg.go.dev, github.com, stackoverflow.com, " +
			"golang.org, developer.mozilla.org, etc.) are accessible. " +
			"Returns sanitized content with source URLs.",
	}, s.handleWebSearch)

	return s
}

// handleWebSearch — обработчик вызова инструмента web_search.
//
// Возвращает CallToolResult с текстовым контентом в формате,
// безопасном для передачи в LLM (обёрнут в маркеры недоверенного контента).
// Out-значение (SearchOutput) автоматически попадает в StructuredContent.
func (s *Server) handleWebSearch(
	ctx context.Context,
	req *mcp.CallToolRequest,
	args SearchArgs,
) (*mcp.CallToolResult, SearchOutput, error) {
	s.log.Info("web_search called", "query", args.Query)

	// Вызываем SafeSearcher — вся безопасность (rate-limit, whitelist,
	// санитизация, anti-prompt-injection) реализована там.
	result, err := s.safeSearcher.Search(ctx, args.Query)
	if err != nil {
		s.log.Warn("web_search failed", "query", args.Query, "err", err)
		// Возвращаем ошибку — SDK сам установит IsError=true и текст ошибки.
		return nil, SearchOutput{}, fmt.Errorf("search error: %w", err)
	}

	// Форматируем результат для prompt с маркерами недоверенного контента.
	formatted := search.FormatForPrompt(result)

	// Собираем структурированный вывод.
	var urls []string
	for _, src := range result.Sources {
		urls = append(urls, src.URL)
	}
	output := SearchOutput{
		Content: result.Content,
		Sources: urls,
	}

	s.log.Info("web_search completed",
		"query", args.Query,
		"sources", len(result.Sources),
		"content_len", len(result.Content),
	)

	// Content заполняем вручную (текст для LLM),
	// StructuredContent SDK заполнит из output автоматически.
	return &mcp.CallToolResult{
		Content: []mcp.Content{
			&mcp.TextContent{Text: formatted},
		},
	}, output, nil
}

// HTTPHandler возвращает http.Handler для Streamable HTTP транспорта.
// Подключается к mux по пути /mcp.
func (s *Server) HTTPHandler() http.Handler {
	return mcp.NewStreamableHTTPHandler(
		func(r *http.Request) *mcp.Server {
			return s.mcpServer
		},
		&mcp.StreamableHTTPOptions{
			Stateless:    true,
			JSONResponse: true,
		},
	)
}