package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/url"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"github.com/gin-gonic/gin"
)

const (
	giphyAPI     = "https://api.giphy.com"
	gifLimit     = 24
	gifRating    = "pg-13"
	gifLang      = "es"
	maxGifQuery  = 50 // runes, after TrimSpace
	maxGiphyBody = 1 << 20
	gifCacheTTL  = 10 * time.Minute
	gifCacheMax  = 100
	giphyTimeout = 5 * time.Second
)

// gif is one picker result: url is sent in chat, preview is the thumbnail.
type gif struct {
	URL     string `json:"url"`
	Preview string `json:"preview"`
}

type gifCacheEntry struct {
	gifs    []gif // never mutated after insertion; shared by readers
	expires time.Time
}

// gifSearch proxies Giphy trending/search. Handlers run concurrently: every field is
// read-only after construction except cache, which only mu guards.
type gifSearch struct {
	baseURL string           // giphyAPI in production, an httptest URL in tests
	key     string           // never logged, never returned
	client  *http.Client     // Timeout: giphyTimeout; tests shorten it
	now     func() time.Time // time.Now; tests advance it

	mu    sync.Mutex
	cache map[string]gifCacheEntry // key: lowercased q ("" = trending)
}

func newGifSearch(baseURL, key string) *gifSearch {
	return &gifSearch{
		baseURL: baseURL,
		key:     key,
		client: &http.Client{
			Timeout: giphyTimeout,
			// A redirect would carry api_key to another host (URL and Referer): a 30x
			// takes the non-200 path instead.
			CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
		},
		now:   time.Now,
		cache: make(map[string]gifCacheEntry),
	}
}

// handle answers GET /gifs?q= with {"gifs":[{"url","preview"}]}.
func (g *gifSearch) handle(c *gin.Context) {
	setStaticHeaders(c)
	q := strings.TrimSpace(c.Query("q"))
	if utf8.RuneCountInString(q) > maxGifQuery {
		c.JSON(http.StatusBadRequest, gin.H{"error": "query_too_long"})
		return
	}
	key := strings.ToLower(q)
	gifs, ok := g.cached(key)
	if !ok {
		var err error
		if gifs, err = g.fetch(c.Request.Context(), key); err != nil {
			log.Printf("gif search: %v", err) // fetch's own sanitized errors: never the URL or key
			c.JSON(http.StatusBadGateway, gin.H{"error": "unavailable"})
			return
		}
		if len(gifs) > 0 { // an empty list is likely transient or fully filtered: retry next time
			g.store(key, gifs)
		}
	}
	c.JSON(http.StatusOK, struct {
		Gifs []gif `json:"gifs"`
	}{gifs})
}

// fetch asks Giphy for trending (q == "") or search results. It never wraps the transport
// error: *url.Error embeds the full request URL, including api_key.
func (g *gifSearch) fetch(ctx context.Context, q string) ([]gif, error) {
	params := url.Values{}
	params.Set("api_key", g.key)
	params.Set("limit", strconv.Itoa(gifLimit))
	params.Set("rating", gifRating)
	path := "/v1/gifs/trending"
	if q != "" {
		path = "/v1/gifs/search"
		params.Set("q", q)
		params.Set("lang", gifLang)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, g.baseURL+path+"?"+params.Encode(), nil)
	if err != nil {
		return nil, errors.New("giphy request failed")
	}
	resp, err := g.client.Do(req)
	if err != nil {
		return nil, errors.New("giphy request failed")
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("giphy status %d", resp.StatusCode)
	}

	var body struct {
		Data []struct {
			Images struct {
				FixedHeight struct {
					URL string `json:"url"`
				} `json:"fixed_height"`
				FixedWidthSmall struct {
					URL string `json:"url"`
				} `json:"fixed_width_small"`
			} `json:"images"`
		} `json:"data"`
	}
	if err := json.NewDecoder(io.LimitReader(resp.Body, maxGiphyBody)).Decode(&body); err != nil {
		return nil, errors.New("giphy response invalid")
	}

	gifs := make([]gif, 0, gifLimit)
	for _, item := range body.Data {
		if len(gifs) >= gifLimit {
			break
		}
		u, ok := canonicalGIF(item.Images.FixedHeight.URL)
		if !ok {
			continue
		}
		p, ok := canonicalGIF(item.Images.FixedWidthSmall.URL)
		if !ok {
			p = u
		}
		gifs = append(gifs, gif{URL: u, Preview: p})
	}
	return gifs, nil
}

func (g *gifSearch) cached(key string) ([]gif, bool) {
	g.mu.Lock()
	defer g.mu.Unlock()
	e, ok := g.cache[key]
	if !ok || !g.now().Before(e.expires) {
		return nil, false
	}
	return e.gifs, true
}

// store inserts gifs; when the cache is full it drops expired entries, then any one entry.
func (g *gifSearch) store(key string, gifs []gif) {
	g.mu.Lock()
	defer g.mu.Unlock()
	now := g.now()
	if _, exists := g.cache[key]; !exists && len(g.cache) >= gifCacheMax {
		for k, e := range g.cache {
			if !now.Before(e.expires) {
				delete(g.cache, k)
			}
		}
		if len(g.cache) >= gifCacheMax {
			for k := range g.cache {
				delete(g.cache, k)
				break
			}
		}
	}
	g.cache[key] = gifCacheEntry{gifs: gifs, expires: now.Add(gifCacheTTL)}
}

// canonicalGIF accepts only https URLs on Giphy hosts (a subset of imageHosts) whose path ends in .gif, with no
// userinfo, port or opaque part, and returns them without query or fragment.
func canonicalGIF(raw string) (string, bool) {
	u, err := url.Parse(raw)
	if err != nil || u.Scheme != "https" || u.Opaque != "" || u.User != nil ||
		!slices.Contains(imageHosts, u.Host) || // exact: rejects "host:443", "host:", "HOST"
		!isGiphyHost(u.Host) ||
		!strings.HasSuffix(strings.ToLower(u.Path), ".gif") {
		return "", false
	}
	u.RawQuery, u.ForceQuery, u.Fragment, u.RawFragment = "", false, "", ""
	return u.String(), true
}

func isGiphyHost(host string) bool {
	return host == "giphy.com" || strings.HasSuffix(host, ".giphy.com")
}
