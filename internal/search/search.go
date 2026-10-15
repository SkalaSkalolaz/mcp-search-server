package search

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"golang.org/x/net/html"
)

type Source struct {
	Title string `json:"title"`
	URL   string `json:"url"`
}

type Result struct {
	Query   string   `json:"query"`
	Content string   `json:"content"`
	Sources []Source `json:"sources"`
}

type Searcher struct {
	http       *http.Client
	log        *slog.Logger
	maxContent int
	backends   []LinkBackend
}

func New(log *slog.Logger) *Searcher {
	s := &Searcher{
		http:       &http.Client{Timeout: 20 * time.Second},
		log:        log,
		maxContent: 30000,
	}
	// Дефолт: только DDG. Позже можно вызвать ConfigureBackendsFromEnv.
	s.backends = []LinkBackend{&DuckDuckGo{HTTP: s.http}}
	return s
}

// ConfigureBackendsFromEnv собирает цепочку бэкендов по переменным окружения:
//
//	SEARXNG_URL    — если задан, добавляется SearXNG (приоритет 1)
//	BRAVE_API_KEY  — если задан, добавляется Brave Search (приоритет 2)
//	DuckDuckGo     — всегда добавляется как последний fallback
func (s *Searcher) ConfigureBackendsFromEnv(getenv func(string) string) {
	var bs []LinkBackend

	if u := getenv("SEARXNG_URL"); u != "" {
		bs = append(bs, &SearXNG{BaseURL: u, HTTP: s.http})
	}
	if k := getenv("BRAVE_API_KEY"); k != "" {
		bs = append(bs, &BraveSearch{APIKey: k, HTTP: s.http})
	}
	bs = append(bs, &DuckDuckGo{HTTP: s.http})
	s.backends = bs

	names := make([]string, 0, len(bs))
	for _, b := range bs {
		names = append(names, b.Name())
	}
	s.log.Info("search backends configured", "chain", strings.Join(names, " → "))
}

func (s *Searcher) SetMaxContent(max int) {
	if max > 0 {
		s.maxContent = max
	}
}

// Search выполняет поиск. urlFilter передаётся как аргумент — так
// один Searcher можно безопасно использовать из нескольких горутин.
func (s *Searcher) Search(
	ctx context.Context,
	query string,
	urlFilter func(string) bool,
) (*Result, error) {
	if len(s.backends) == 0 {
		return nil, fmt.Errorf("no search backends configured")
	}

	// ── 1. Собираем ссылки со всех бэкендов, пока не наберём достаточно ──
	var links []Source
	var backendErrs []string
	for _, b := range s.backends {
		got, err := b.Search(ctx, query, 20)
		if err != nil {
			backendErrs = append(backendErrs, fmt.Sprintf("%s: %v", b.Name(), err))
			s.log.Warn("search backend failed", "backend", b.Name(), "err", err)
			continue
		}
		s.log.Info("search backend ok", "backend", b.Name(), "links", len(got))
		links = append(links, got...)
		if len(links) >= 20 {
			break
		}
	}
	if len(links) == 0 {
		return nil, fmt.Errorf("no search links found (%s)",
			strings.Join(backendErrs, "; "))
	}

	// ── 2. Качаем контент ────────────────────────────────────────
	result := &Result{Query: query}
	var contentParts []string
	total := 0
	maxTotal := s.maxContent
	if maxTotal <= 0 {
		maxTotal = 30000
	}

	seen := map[string]bool{}
	for _, link := range links {
		if total >= maxTotal {
			break
		}
		if seen[link.URL] {
			continue
		}
		seen[link.URL] = true
		if urlFilter != nil && !urlFilter(link.URL) {
			s.log.Debug("search URL filtered out", "url", link.URL)
			continue
		}
		text, err := s.fetchText(ctx, link.URL)
		if err != nil {
			s.log.Warn("search fetch failed", "url", link.URL, "err", err)
			continue
		}
		if strings.TrimSpace(text) == "" {
			continue
		}
		remaining := maxTotal - total
		if len(text) > remaining {
			text = TruncateStringBytes(text, remaining)
		}
		contentParts = append(contentParts,
			fmt.Sprintf("SOURCE: %s\nURL: %s\n%s", link.Title, link.URL, text))
		result.Sources = append(result.Sources, link)
		total += len(text)
	}

	if len(contentParts) == 0 {
		return nil, fmt.Errorf(
			"no usable content fetched from %d candidate links", len(links))
	}
	result.Content = strings.Join(contentParts, "\n\n")
	return result, nil
}

func (s *Searcher) fetchText(ctx context.Context, pageURL string) (string, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, pageURL, nil)
	if err != nil {
		return "", err
	}
	req.Header.Set("User-Agent",
		"Mozilla/5.0 (X11; Linux x86_64) AppleWebKit/537.36 "+
			"(KHTML, like Gecko) Chrome/122.0.0.0 Safari/537.36")
	req.Header.Set("Accept", "text/html,application/xhtml+xml")

	resp, err := s.http.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("HTTP %d", resp.StatusCode)
	}

	doc, err := html.Parse(io.LimitReader(resp.Body, 2<<20))
	if err != nil {
		return "", err
	}

	text := extractText(doc)
	text = normalizeWhitespace(text)
	if len(text) > 10000 {
		text = TruncateStringBytes(text, 10000)
	}
	return text, nil
}

func extractText(n *html.Node) string {
	var b strings.Builder
	var walk func(*html.Node)
	walk = func(node *html.Node) {
		if node.Type == html.ElementNode {
			switch node.Data {
			case "script", "style", "nav", "header", "footer", "noscript":
				return
			}
		}
		if node.Type == html.TextNode {
			text := strings.TrimSpace(node.Data)
			if text != "" {
				b.WriteString(text)
				b.WriteByte(' ')
			}
		}
		for c := node.FirstChild; c != nil; c = c.NextSibling {
			walk(c)
		}
	}
	walk(n)
	return b.String()
}

func normalizeWhitespace(s string) string {
	return strings.Join(strings.Fields(s), " ")
}