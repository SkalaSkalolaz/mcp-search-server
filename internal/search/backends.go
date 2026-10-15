package search

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"

	"golang.org/x/net/html"
)

// LinkBackend — источник ссылок по поисковому запросу.
type LinkBackend interface {
	Name() string
	Search(ctx context.Context, query string, max int) ([]Source, error)
}

// ────────────────────────────────────────────────────────────────
// SearXNG — самый надёжный вариант. Поднимается локально
// (docker run -p 8888:8080 searxng/searxng) или используется
// публичный инстанс. Никаких API-ключей.
//
// Активируется переменной SEARXNG_URL (например,
// http://127.0.0.1:8888). В SearXNG нужно включить JSON-формат:
// в settings.yml: search.formats: [html, json]
// ────────────────────────────────────────────────────────────────

type SearXNG struct {
	BaseURL string
	HTTP    *http.Client
}

func (s *SearXNG) Name() string { return "searxng" }

func (s *SearXNG) Search(ctx context.Context, query string, max int) ([]Source, error) {
	u := strings.TrimRight(s.BaseURL, "/") +
		"/search?q=" + url.QueryEscape(query) +
		"&format=json&safesearch=1"

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", "Mozilla/5.0 (compatible; Gogitor/1.0)")
	req.Header.Set("Accept", "application/json")

	resp, err := s.HTTP.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("searxng HTTP %d", resp.StatusCode)
	}

	var payload struct {
		Results []struct {
			URL   string `json:"url"`
			Title string `json:"title"`
		} `json:"results"`
	}
	if err := json.NewDecoder(io.LimitReader(resp.Body, 4<<20)).Decode(&payload); err != nil {
		return nil, fmt.Errorf("searxng decode: %w", err)
	}

	out := make([]Source, 0, max)
	for _, r := range payload.Results {
		if len(out) >= max {
			break
		}
		if r.URL == "" || !strings.HasPrefix(r.URL, "http") {
			continue
		}
		out = append(out, Source{Title: r.Title, URL: r.URL})
	}
	return out, nil
}

// ────────────────────────────────────────────────────────────────
// Brave Search API — надёжный, но требует ключ.
// Активируется переменной BRAVE_API_KEY.
// ────────────────────────────────────────────────────────────────

type BraveSearch struct {
	APIKey string
	HTTP   *http.Client
}

func (b *BraveSearch) Name() string { return "brave" }

func (b *BraveSearch) Search(ctx context.Context, query string, max int) ([]Source, error) {
	u := "https://api.search.brave.com/res/v1/web/search?q=" +
		url.QueryEscape(query) + "&count=" + fmt.Sprint(max)

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Accept", "application/json")
	req.Header.Set("X-Subscription-Token", b.APIKey)

	resp, err := b.HTTP.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("brave HTTP %d", resp.StatusCode)
	}

	var payload struct {
		Web struct {
			Results []struct {
				URL   string `json:"url"`
				Title string `json:"title"`
			} `json:"results"`
		} `json:"web"`
	}
	if err := json.NewDecoder(io.LimitReader(resp.Body, 8<<20)).Decode(&payload); err != nil {
		return nil, fmt.Errorf("brave decode: %w", err)
	}

	out := make([]Source, 0, max)
	for _, r := range payload.Web.Results {
		if len(out) >= max {
			break
		}
		if r.URL == "" {
			continue
		}
		out = append(out, Source{Title: r.Title, URL: r.URL})
	}
	return out, nil
}

// ────────────────────────────────────────────────────────────────
// DuckDuckGo — бесплатный, без ключей, но хрупкий.
// Пробуем ДВА эндпоинта подряд: html.duckduckgo.com и
// lite.duckduckgo.com. Lite проще по разметке и реже блокируется.
// ────────────────────────────────────────────────────────────────

type DuckDuckGo struct {
	HTTP *http.Client
}

func (d *DuckDuckGo) Name() string { return "duckduckgo" }

func (d *DuckDuckGo) Search(ctx context.Context, query string, max int) ([]Source, error) {
	endpoints := []string{
		"https://html.duckduckgo.com/html/?q=",
		"https://lite.duckduckgo.com/lite/?q=",
	}

	var lastErr error
	for _, ep := range endpoints {
		links, err := d.searchOne(ctx, ep+url.QueryEscape(query), max)
		if err != nil {
			lastErr = err
			continue
		}
		if len(links) > 0 {
			return links, nil
		}
		lastErr = fmt.Errorf("empty result from %s", ep)
	}
	if lastErr == nil {
		lastErr = fmt.Errorf("duckduckgo returned nothing")
	}
	return nil, lastErr
}

func (d *DuckDuckGo) searchOne(ctx context.Context, searchURL string, max int) ([]Source, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, searchURL, nil)
	if err != nil {
		return nil, err
	}
	// Реальный браузерный UA — снижает вероятность капчи.
	req.Header.Set("User-Agent",
		"Mozilla/5.0 (X11; Linux x86_64) AppleWebKit/537.36 "+
			"(KHTML, like Gecko) Chrome/122.0.0.0 Safari/537.36")
	req.Header.Set("Accept", "text/html,application/xhtml+xml")
	req.Header.Set("Accept-Language", "en-US,en;q=0.9")

	resp, err := d.HTTP.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("duckduckgo HTTP %d", resp.StatusCode)
	}

	doc, err := html.Parse(io.LimitReader(resp.Body, 4<<20))
	if err != nil {
		return nil, err
	}

	var links []Source
	seen := map[string]bool{}

	var walk func(*html.Node)
	walk = func(n *html.Node) {
		if len(links) >= max {
			return
		}
		if n.Type == html.ElementNode && n.Data == "a" {
			var href, class string
			for _, attr := range n.Attr {
				switch attr.Key {
				case "href":
					href = attr.Val
				case "class":
					class = attr.Val
				}
			}
			// html.duckduckgo.com → class="result__a"
			// lite.duckduckgo.com → href вида "//duckduckgo.com/l/?uddg=..."
			//                       или "https://duckduckgo.com/l/?uddg=..."
			isResult := strings.Contains(class, "result__a") ||
				(strings.Contains(href, "uddg=") &&
					strings.Contains(href, "duckduckgo.com/l/"))

			if isResult && href != "" {
				final := decodeDDGURL(href)
				if final == "" || strings.Contains(final, "duckduckgo.com") {
					// пропускаем внутренние ссылки DDG
				} else if !seen[final] {
					seen[final] = true
					links = append(links, Source{
						Title: strings.TrimSpace(extractText(n)),
						URL:   final,
					})
				}
			}
		}
		for c := n.FirstChild; c != nil; c = c.NextSibling {
			walk(c)
		}
	}
	walk(doc)
	return links, nil
}

// decodeDDGURL распаковывает редирект-обёртку DDG в реальный URL.
func decodeDDGURL(href string) string {
	if strings.HasPrefix(href, "//") {
		href = "https:" + href
	}
	if !strings.HasPrefix(href, "http") {
		return ""
	}
	u, err := url.Parse(href)
	if err != nil {
		return ""
	}
	if uddg := u.Query().Get("uddg"); uddg != "" {
		if decoded, err := url.QueryUnescape(uddg); err == nil {
			return decoded
		}
	}
	return href
}