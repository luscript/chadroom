package main

import (
	"context"
	_ "embed"
	"errors"
	"log"
	"net"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

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

// imageHosts may be shown inline in the chat (CSP img-src).
// Mirrored by IMAGE_HOSTS in app.js; TestImageHostsMirroredInJS keeps them in sync.
var imageHosts = []string{
	"media.giphy.com", "media0.giphy.com", "media1.giphy.com", "media2.giphy.com",
	"media3.giphy.com", "media4.giphy.com", "i.giphy.com",
	"media.tenor.com", "i.imgur.com",
}

// roomIDs is the fixed room set, in display order; the first is the default room.
var roomIDs = []string{"general", "juegos", "musica"}

var contentSecurityPolicy = buildCSP(imageHosts)

// buildCSP renders the page's Content-Security-Policy: everything is same-origin
// except the image hosts.
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
		rooms[i] = chat.NewRoom(id)
	}
	return chat.NewServer(allowedOrigins, rooms...)
}

// serveRooms returns the room list as {"rooms":[...]}.
func serveRooms(c *gin.Context) {
	setStaticHeaders(c)
	c.JSON(http.StatusOK, struct {
		Rooms []string `json:"rooms"`
	}{roomIDs})
}

// newRouter registers the HTML page, its static assets, the room list and the WebSocket endpoint.
// /gifs is registered only when gifs is non-nil, so without a key it is a plain 404.
func newRouter(chatSrv *chat.Server, gifs *gifSearch) *gin.Engine {
	r := gin.Default()
	if err := r.SetTrustedProxies(nil); err != nil {
		log.Printf("set trusted proxies: %v", err)
	}
	r.GET("/", servePage)
	r.GET("/app.js", serveAsset("text/javascript; charset=utf-8", appJS))
	r.GET("/app.css", serveAsset("text/css; charset=utf-8", appCSS))
	r.GET("/rooms", serveRooms)
	r.GET("/ws", chatSrv.HandleWebSocket)
	if gifs != nil {
		r.GET("/gifs", gifs.handle)
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
}

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	code := serve(ctx, addr, config{
		allowedOrigins: os.Getenv("ALLOWED_ORIGINS"),
		giphyKey:       strings.TrimSpace(os.Getenv("GIPHY_API_KEY")),
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
	roomsDone := make(chan struct{})
	go func() {
		chatSrv.Run(ctx)
		close(roomsDone)
	}()

	srv := &http.Server{
		Handler:           newRouter(chatSrv, gifs),
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
