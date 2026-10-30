package main

import (
	"context"
	_ "embed"
	"errors"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"net/url"
	"os"
	"os/signal"
	"regexp"
	"slices"
	"strings"
	"syscall"
	"time"
	"unicode"
	"unicode/utf8"

	"chatroom/internal/chat"

	"github.com/gin-gonic/gin"
)

const addr = ":8080"

//go:embed index.html
var indexHTML []byte

//go:embed app.js
var appJS []byte

//go:embed app.css
var appCSS []byte

//go:embed favicon.svg
var faviconSVG []byte

//go:embed favicon-unread.svg
var faviconUnreadSVG []byte

// imageHosts may be shown inline in the chat (CSP img-src).
// Mirrored by IMAGE_HOSTS in app.js; TestImageHostsMirroredInJS keeps them in sync.
var imageHosts = []string{
	"media.giphy.com", "media0.giphy.com", "media1.giphy.com", "media2.giphy.com",
	"media3.giphy.com", "media4.giphy.com", "i.giphy.com",
	"media.tenor.com", "i.imgur.com",
}

const maxAvatarBytes = 512 // mirrored by MAX_AVATAR_BYTES in app.js

// avatarPath mirrors IMAGE_PATH in app.js.
var avatarPath = regexp.MustCompile(`(?i)\.(gif|webp|png|jpe?g)$`)

// validAvatar accepts the same URLs the page renders inline (isAllowedImage): https, an exact
// imageHosts host (so no port), no userinfo, no fragment, an image extension, at most
// maxAvatarBytes (CHAT-28). The URL is relayed exactly as given; it is never rewritten.
func validAvatar(raw string) bool {
	if raw == "" || len(raw) > maxAvatarBytes || !utf8.ValidString(raw) || strings.Contains(raw, "#") ||
		strings.ContainsFunc(raw, unicode.IsSpace) {
		return false
	}
	u, err := url.Parse(raw)
	return err == nil && u.Scheme == "https" && u.Opaque == "" && u.User == nil &&
		slices.Contains(imageHosts, u.Host) && avatarPath.MatchString(u.EscapedPath())
}

// roomIDs is the fixed room set, in display order; the first is the default room.
var roomIDs = []string{"general", "juegos", musicRoom}

// musicRoom is the room with the shared YouTube player (CHAT-30); mirrored by MUSIC_ROOM in app.js.
const musicRoom = "musica"

// youtubeEmbedHost is the only frame source: the player embed, which sets no cookies until played (SRV-05).
const youtubeEmbedHost = "https://www.youtube-nocookie.com"

var contentSecurityPolicy = buildCSP(imageHosts)

// buildCSP renders the page's Content-Security-Policy: everything is same-origin
// except the image hosts and the YouTube embed frame.
func buildCSP(hosts []string) string {
	img := []string{"img-src", "'self'"}
	for _, h := range hosts {
		img = append(img, "https://"+h)
	}
	return strings.Join([]string{
		"default-src 'none'",
		"script-src 'self'",
		"style-src 'self'",
		strings.Join(img, " "),
		"connect-src 'self'",
		"frame-src " + youtubeEmbedHost,
		"base-uri 'none'",
		"form-action 'none'",
		"frame-ancestors 'none'",
	}, "; ")
}

// setStaticHeaders applies the headers every embedded response shares.
func setStaticHeaders(c *gin.Context) {
	c.Header("X-Content-Type-Options", "nosniff")
	c.Header("Cache-Control", "no-cache")
}

// servePage serves the chat page with its CSP and framing protection.
func servePage(c *gin.Context) {
	setStaticHeaders(c)
	c.Header("Content-Security-Policy", contentSecurityPolicy)
	c.Header("X-Frame-Options", "DENY")
	c.Header("Referrer-Policy", "no-referrer")
	c.Data(http.StatusOK, "text/html; charset=utf-8", indexHTML)
}

// serveAsset serves an embedded static file with the given content type.
func serveAsset(contentType string, body []byte) gin.HandlerFunc {
	return func(c *gin.Context) {
		setStaticHeaders(c)
		c.Data(http.StatusOK, contentType, body)
	}
}

// newChatServer builds one Room per roomIDs entry behind a shared origin policy.
func newChatServer(allowedOrigins []string) *chat.Server {
	rooms := make([]*chat.Room, len(roomIDs))
	for i, id := range roomIDs {
		if id == musicRoom {
			rooms[i] = chat.NewMusicRoom(id)
		} else {
			rooms[i] = chat.NewRoom(id)
		}
	}
	return chat.NewServer(allowedOrigins, validAvatar, rooms...)
}

// serveRooms returns the room list as {"rooms":[...]}.
func serveRooms(c *gin.Context) {
	setStaticHeaders(c)
	c.JSON(http.StatusOK, struct {
		Rooms []string `json:"rooms"`
	}{roomIDs})
}

// newRouter registers the HTML page, its static assets, the room list and the WebSocket endpoint.
// /gifs and /playlist are registered only when gifs / playlists are non-nil, so without their key
// they are a plain 404.
func newRouter(chatSrv *chat.Server, gifs *gifSearch, playlists *playlistImport) *gin.Engine {
	return newRouterLogging(chatSrv, gifs, playlists, gin.DefaultWriter)
}

// logFormatter is gin's default log line but without the query string: /ws carries the
// nickname, avatar and bio there, and none of it belongs in the logs.
func logFormatter(p gin.LogFormatterParams) string {
	path, _, _ := strings.Cut(p.Path, "?")
	return fmt.Sprintf("[GIN] %s | %3d | %13v | %15s | %-7s %#v\n%s",
		p.TimeStamp.Format("2006/01/02 - 15:04:05"), p.StatusCode, p.Latency, p.ClientIP, p.Method, path, p.ErrorMessage)
}

// newRouterLogging is newRouter writing its request log to logw.
func newRouterLogging(chatSrv *chat.Server, gifs *gifSearch, playlists *playlistImport, logw io.Writer) *gin.Engine {
	r := gin.New()
	r.Use(gin.LoggerWithConfig(gin.LoggerConfig{Output: logw, Formatter: logFormatter}), gin.Recovery())
	if err := r.SetTrustedProxies(nil); err != nil {
		log.Printf("set trusted proxies: %v", err)
	}
	r.GET("/", servePage)
	r.GET("/app.js", serveAsset("text/javascript; charset=utf-8", appJS))
	r.GET("/app.css", serveAsset("text/css; charset=utf-8", appCSS))
	r.GET("/favicon.svg", serveAsset("image/svg+xml", faviconSVG))
	r.GET("/favicon-unread.svg", serveAsset("image/svg+xml", faviconUnreadSVG))
	r.GET("/rooms", serveRooms)
	r.GET("/ws", chatSrv.HandleWebSocket)
	if gifs != nil {
		r.GET("/gifs", gifs.handle)
	}
	if playlists != nil {
		r.GET("/playlist", playlists.handle)
	}
	return r
}

// parseAllowedOrigins splits a comma-separated list, trimming blanks and
// rejecting the "null" origin, which must never be allowed.
func parseAllowedOrigins(s string) []string {
	origins := []string{}
	for _, o := range strings.Split(s, ",") {
		o = strings.TrimSpace(o)
		if o == "" {
			continue
		}
		if o == "null" {
			log.Println("ignoring \"null\" in ALLOWED_ORIGINS")
			continue
		}
		origins = append(origins, o)
	}
	return origins
}

// config is what main reads from the environment.
type config struct {
	allowedOrigins string // ALLOWED_ORIGINS, comma-separated
	giphyKey       string // GIPHY_API_KEY; "" disables /gifs
	youtubeKey     string // YOUTUBE_API_KEY; "" disables /playlist
}

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	code := serve(ctx, addr, config{
		allowedOrigins: os.Getenv("ALLOWED_ORIGINS"),
		giphyKey:       strings.TrimSpace(os.Getenv("GIPHY_API_KEY")),
		youtubeKey:     strings.TrimSpace(os.Getenv("YOUTUBE_API_KEY")),
	})
	stop() // os.Exit skips defers.
	os.Exit(code)
}

// serve binds addr and runs the server, returning the process exit code.
func serve(ctx context.Context, addr string, cfg config) int {
	ln, err := net.Listen("tcp", addr)
	if err != nil {
		log.Printf("listen error: %v", err)
		return 1
	}
	return run(ctx, ln, cfg)
}

// run serves on ln until ctx is cancelled and returns the process exit code.
func run(ctx context.Context, ln net.Listener, cfg config) int {
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	chatSrv := newChatServer(parseAllowedOrigins(cfg.allowedOrigins))
	var gifs *gifSearch
	if cfg.giphyKey != "" {
		gifs = newGifSearch(giphyAPI, cfg.giphyKey)
	} else {
		log.Println("GIPHY_API_KEY not set: GIF search disabled")
	}
	var playlists *playlistImport
	if cfg.youtubeKey != "" {
		playlists = newPlaylistImport(youtubeAPI, cfg.youtubeKey)
	} else {
		log.Println("YOUTUBE_API_KEY not set: playlist import disabled")
	}
	roomsDone := make(chan struct{})
	go func() {
		chatSrv.Run(ctx)
		close(roomsDone)
	}()

	srv := &http.Server{
		Handler:           newRouter(chatSrv, gifs, playlists),
		ReadHeaderTimeout: 10 * time.Second,
		IdleTimeout:       60 * time.Second,
		MaxHeaderBytes:    8 << 10,
	}
	errCh := make(chan error, 1)
	go func() { errCh <- srv.Serve(ln) }()
	log.Printf("listening on %s", ln.Addr())

	select {
	case err := <-errCh:
		if err != nil && !errors.Is(err, http.ErrServerClosed) {
			log.Printf("server error: %v", err)
			return 1
		}
	case <-ctx.Done():
	}

	cancel()    // stop the rooms on the server-error path too.
	<-roomsDone // every room closes its WebSocket connections; Shutdown does not.
	shCtx, shCancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer shCancel()
	if err := srv.Shutdown(shCtx); err != nil {
		log.Printf("shutdown error: %v", err)
	}
	return 0
}
