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
	"regexp"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/gin-gonic/gin"
)

const (
	youtubeAPI       = "https://www.googleapis.com/youtube/v3"
	playlistMax      = 50 // playlistItems.list maxResults ceiling; first page only
	maxYouTubeBody   = 1 << 20
	playlistCacheTTL = 10 * time.Minute
	playlistCacheMax = 100
	youtubeTimeout   = 5 * time.Second // the whole import (both calls), not each call

	youtubeKeyHeader = "X-Goog-Api-Key"
	reasonNotAccess  = "playlistItemsNotAccessible"
)

var (
	playlistID     = regexp.MustCompile(`^[A-Za-z0-9_-]{10,64}$`) // mirrored by YT_LIST_ID in app.js
	youtubeVideoID = regexp.MustCompile(`^[A-Za-z0-9_-]{11}$`)    // same rule as chat's videoID
)

var errPlaylistNotFound = errors.New("playlist not found")

// youtubeStatusError is a non-200 upstream answer. Its text is only the code: never upstream text.
type youtubeStatusError struct {
	code    int
	reasons []string
}

func (e youtubeStatusError) Error() string { return fmt.Sprintf("youtube status %d", e.code) }

type playlistCacheEntry struct {
	videos  []string // never mutated after insertion; shared by readers
	expires time.Time
}

// playlistImport proxies YouTube playlist expansion. Handlers run concurrently: every field is
// read-only after construction except cache, which only mu guards.
type playlistImport struct {
	baseURL string           // youtubeAPI in production, an httptest URL in tests
	key     string           // never logged, never returned, never in a URL
	client  *http.Client     // no redirects; Timeout youtubeTimeout as a backstop
	timeout time.Duration    // deadline for the whole import; tests shorten it
	now     func() time.Time // time.Now; tests advance it

	mu    sync.Mutex
	cache map[string]playlistCacheEntry // key: the list id, case-sensitive
}

func newPlaylistImport(baseURL, key string) *playlistImport {
	return &playlistImport{
		baseURL: baseURL,
		key:     key,
		client: &http.Client{
			Timeout: youtubeTimeout,
			// Go forwards custom headers (X-Goog-Api-Key) on a cross-host redirect: a 30x
			// takes the non-200 path instead.
			CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
		},
		timeout: youtubeTimeout,
		now:     time.Now,
		cache:   make(map[string]playlistCacheEntry),
	}
}

// handle answers GET /playlist?list= with {"videos":["<id>",...]}.
func (p *playlistImport) handle(c *gin.Context) {
	setStaticHeaders(c)
	list := c.Query("list")
	if !playlistID.MatchString(list) {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid_list"})
		return
	}
	videos, ok := p.cached(list)
	if !ok {
		var err error
		if videos, err = p.fetch(c.Request.Context(), list); err != nil {
			if errors.Is(err, errPlaylistNotFound) {
				c.JSON(http.StatusNotFound, gin.H{"error": "not_found"})
				return
			}
			log.Printf("playlist import: %v", err) // fetch's own sanitized errors: never the key
			c.JSON(http.StatusBadGateway, gin.H{"error": "unavailable"})
			return
		}
		if len(videos) > 0 { // an empty list may be transient or fully filtered: retry next time
			p.store(list, videos)
		}
	}
	c.JSON(http.StatusOK, struct {
		Videos []string `json:"videos"`
	}{videos})
}

// fetch expands a playlist into playable video ids, in playlist order. Both upstream calls share
// one deadline. The result is never nil.
func (p *playlistImport) fetch(ctx context.Context, list string) ([]string, error) {
	ctx, cancel := context.WithTimeout(ctx, p.timeout)
	defer cancel()

	var items struct {
		Items []struct {
			ContentDetails struct {
				VideoID string `json:"videoId"`
			} `json:"contentDetails"`
		} `json:"items"`
	}
	err := p.get(ctx, "/playlistItems", url.Values{
		"part":       {"contentDetails"},
		"playlistId": {list},
		"maxResults": {strconv.Itoa(playlistMax)},
		"fields":     {"items/contentDetails/videoId"},
	}, &items)
	if err != nil {
		var se youtubeStatusError
		if errors.As(err, &se) && (se.code == http.StatusNotFound ||
			se.code == http.StatusForbidden && slices.Contains(se.reasons, reasonNotAccess)) {
			return nil, errPlaylistNotFound
		}
		return nil, err
	}

	ids := make([]string, 0, playlistMax)
	for _, it := range items.Items {
		id := it.ContentDetails.VideoID
		if len(ids) >= playlistMax {
			break
		}
		if youtubeVideoID.MatchString(id) && !slices.Contains(ids, id) {
			ids = append(ids, id)
		}
	}
	if len(ids) == 0 {
		return ids, nil
	}

	var vids struct {
		Items []struct {
			ID     string `json:"id"`
			Status struct {
				UploadStatus  string `json:"uploadStatus"`
				PrivacyStatus string `json:"privacyStatus"`
				Embeddable    bool   `json:"embeddable"`
			} `json:"status"`
		} `json:"items"`
	}
	err = p.get(ctx, "/videos", url.Values{
		"part":   {"status"},
		"id":     {strings.Join(ids, ",")},
		"fields": {"items(id,status(uploadStatus,privacyStatus,embeddable))"},
	}, &vids)
	if err != nil {
		return nil, err
	}
	playable := make(map[string]bool, len(vids.Items))
	for _, v := range vids.Items {
		s := v.Status
		if s.UploadStatus == "processed" && (s.PrivacyStatus == "public" || s.PrivacyStatus == "unlisted") && s.Embeddable {
			playable[v.ID] = true
		}
	}
	// Absent from videos.list means unavailable (fail closed); ids we did not ask for are ignored.
	return slices.DeleteFunc(ids, func(id string) bool { return !playable[id] }), nil
}

// get performs one upstream call and decodes its 200 body into out. It never wraps the
// transport error and never includes upstream text in its errors.
func (p *playlistImport) get(ctx context.Context, path string, q url.Values, out any) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, p.baseURL+path+"?"+q.Encode(), nil)
	if err != nil {
		return errors.New("youtube request failed")
	}
	req.Header.Set(youtubeKeyHeader, p.key)
	resp, err := p.client.Do(req)
	if err != nil {
		return errors.New("youtube request failed")
	}
	defer resp.Body.Close()
	body := io.LimitReader(resp.Body, maxYouTubeBody)
	if resp.StatusCode != http.StatusOK {
		se := youtubeStatusError{code: resp.StatusCode}
		if resp.StatusCode == http.StatusForbidden { // best effort: only the reasons are read
			var env struct {
				Error struct {
					Errors []struct {
						Reason string `json:"reason"`
					} `json:"errors"`
				} `json:"error"`
			}
			if json.NewDecoder(body).Decode(&env) == nil {
				for _, e := range env.Error.Errors {
					se.reasons = append(se.reasons, e.Reason)
				}
			}
		}
		return se
	}
	if err := json.NewDecoder(body).Decode(out); err != nil {
		return errors.New("youtube response invalid")
	}
	return nil
}

func (p *playlistImport) cached(list string) ([]string, bool) {
	p.mu.Lock()
	defer p.mu.Unlock()
	e, ok := p.cache[list]
	if !ok || !p.now().Before(e.expires) {
		return nil, false
	}
	return e.videos, true
}

// store inserts videos; when the cache is full it drops expired entries, then any one entry.
func (p *playlistImport) store(list string, videos []string) {
	p.mu.Lock()
	defer p.mu.Unlock()
	now := p.now()
	if _, exists := p.cache[list]; !exists && len(p.cache) >= playlistCacheMax {
		for k, e := range p.cache {
			if !now.Before(e.expires) {
				delete(p.cache, k)
			}
		}
		if len(p.cache) >= playlistCacheMax {
			for k := range p.cache {
				delete(p.cache, k)
				break
			}
		}
	}
	p.cache[list] = playlistCacheEntry{videos: videos, expires: now.Add(playlistCacheTTL)}
}
