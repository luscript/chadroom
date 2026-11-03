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

	"chatroom/internal/auth"
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

//go:embed denied.html
var deniedHTML []byte

//go:embed login.html
var loginHTML []byte

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

// authPageCSP is for the login and denied pages: plain markup, a stylesheet and links, no
// script, no connections, no frames.
const authPageCSP = "default-src 'none'; style-src 'self'; img-src 'self'; base-uri 'none'; form-action 'none'; frame-ancestors 'none'"

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

// serveAuthPage serves an embedded auth page with its CSP. Never cached: it follows a login attempt.
func serveAuthPage(status int, body []byte) gin.HandlerFunc {
	return func(c *gin.Context) {
		setStaticHeaders(c)
		c.Header("Cache-Control", "no-store")
		c.Header("Content-Security-Policy", authPageCSP)
		c.Header("X-Frame-Options", "DENY")
		c.Header("Referrer-Policy", "no-referrer")
		c.Data(status, "text/html; charset=utf-8", body)
	}
}

// identityNickname is the chat nickname of the authenticated user: the display name, or the email
// local part when the account has none. Empty without an identity (unreachable behind Require).
func identityNickname(r *http.Request) string {
	id, ok := auth.IdentityFrom(r.Context())
	if !ok {
		return ""
	}
	// CHAT-41: the name counts only if something is left of it once the chat's nickname rules apply.
	if name := strings.TrimSpace(id.Name); chat.CleanNickname(name) != "" {
		return name
	}
	if i := strings.LastIndexByte(id.Email, '@'); i > 0 {
		return id.Email[:i]
	}
	return id.Email
}

// recoverPanics turns a handler panic into a 500. gin.Recovery dumps the request headers, Cookie
// included, so this logs only the panic value, the method and the path without its query (SRV-18).
func recoverPanics(c *gin.Context) {
	defer func() {
		if v := recover(); v != nil {
			if v == http.ErrAbortHandler {
				panic(v) // net/http's own way to abort a response quietly
			}
			log.Printf("panic: %v (%s %s)", v, c.Request.Method, c.Request.URL.Path)
			c.AbortWithStatus(http.StatusInternalServerError)
		}
	}()
	c.Next()
}

// serveAsset serves an embedded static file with the given content type.
func serveAsset(contentType string, body []byte) gin.HandlerFunc {
	return func(c *gin.Context) {
		setStaticHeaders(c)
		c.Data(http.StatusOK, contentType, body)
	}
}

// newChatServer builds one Room per roomIDs entry behind a shared origin policy.
func newChatServer(allowedOrigins []string, nickname chat.NicknameFunc) *chat.Server {
	rooms := make([]*chat.Room, len(roomIDs))
	for i, id := range roomIDs {
		if id == musicRoom {
			rooms[i] = chat.NewMusicRoom(id)
		} else {
			rooms[i] = chat.NewRoom(id)
		}
	}
	return chat.NewServer(allowedOrigins, validAvatar, nickname, rooms...)
}

// serveRooms returns the room list as {"rooms":[...]}.
func serveRooms(c *gin.Context) {
	setStaticHeaders(c)
	c.JSON(http.StatusOK, struct {
		Rooms []string `json:"rooms"`
	}{roomIDs})
}

// guards is what protects the router: the session checks for pages and APIs and the /auth routes.
// newRouter panics on a missing field, so no router can run unauthenticated by accident.
//
// page picks the handler for "/": chat with a valid session, login otherwise. The chat page is
// never served by anything but its first argument, and only behind a verified session.
type guards struct {
	page     func(chat, login gin.HandlerFunc) gin.HandlerFunc
	api      gin.HandlerFunc
	register func(*gin.RouterGroup)
}

// authGuards builds the guards from the auth handler; run always uses it.
func authGuards(a *auth.Handler) guards {
	sessions := a.Sessions()
	return guards{
		page: func(chat, login gin.HandlerFunc) gin.HandlerFunc {
			return func(c *gin.Context) {
				// Same URL, two bodies: caches must key on the cookie.
				c.Header("Vary", "Cookie")
				if _, ok := sessions.Identify(c.Request); ok {
					chat(c)
					return
				}
				login(c)
			}
		},
		api:      sessions.Require(),
		register: a.Register,
	}
}

// newRouter registers the pages, static assets, room list and WebSocket endpoint behind g.
// /gifs and /playlist are registered only when gifs / playlists are non-nil, so without their key
// they are a plain 404.
func newRouter(chatSrv *chat.Server, gifs *gifSearch, playlists *playlistImport, g guards) *gin.Engine {
	return newRouterLogging(chatSrv, gifs, playlists, g, gin.DefaultWriter)
}

// logFormatter is gin's default log line but without the query string: /ws carries the
// nickname, avatar and bio there, and none of it belongs in the logs.
func logFormatter(p gin.LogFormatterParams) string {
	path, _, _ := strings.Cut(p.Path, "?")
	return fmt.Sprintf("[GIN] %s | %3d | %13v | %15s | %-7s %#v\n%s",
		p.TimeStamp.Format("2006/01/02 - 15:04:05"), p.StatusCode, p.Latency, p.ClientIP, p.Method, path, p.ErrorMessage)
}

// newRouterLogging is newRouter writing its request log to logw.
func newRouterLogging(chatSrv *chat.Server, gifs *gifSearch, playlists *playlistImport, g guards, logw io.Writer) *gin.Engine {
	if g.page == nil || g.api == nil || g.register == nil {
		panic("newRouter: incomplete guards")
	}
	r := gin.New()
	r.Use(gin.LoggerWithConfig(gin.LoggerConfig{Output: logw, Formatter: logFormatter}), recoverPanics)
	if err := r.SetTrustedProxies(nil); err != nil {
		log.Printf("set trusted proxies: %v", err)
	}
	page, api := g.page, g.api
	// Protected: the session check runs before the handler, so /ws answers 401 before any room 404
	// or origin 403 (SRV-06). /app.js is a script fetch, which cannot follow a login redirect.
	// "/" shows the login page instead of a 401 when there is no session.
	r.GET("/", page(servePage, serveAuthPage(http.StatusOK, loginHTML)))
	r.GET("/app.js", api, serveAsset("text/javascript; charset=utf-8", appJS))
	r.GET("/rooms", api, serveRooms)
	r.GET("/ws", api, chatSrv.HandleWebSocket)
	if gifs != nil {
		r.GET("/gifs", api, gifs.handle)
	}
	if playlists != nil {
		r.GET("/playlist", api, playlists.handle)
	}
	// Public: the login page itself needs the stylesheet and favicons.
	r.GET("/app.css", serveAsset("text/css; charset=utf-8", appCSS))
	r.GET("/favicon.svg", serveAsset("image/svg+xml", faviconSVG))
	r.GET("/favicon-unread.svg", serveAsset("image/svg+xml", faviconUnreadSVG))
	ag := r.Group("/auth")
	g.register(ag)
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
	auth           auth.Config
}

func main() {
	authCfg, err := auth.LoadConfig(os.Getenv)
	if err != nil {
		log.Printf("invalid auth configuration: %v", err) // names variables, never values
		os.Exit(1)
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	code := serve(ctx, addr, config{
		allowedOrigins: os.Getenv("ALLOWED_ORIGINS"),
		giphyKey:       strings.TrimSpace(os.Getenv("GIPHY_API_KEY")),
		youtubeKey:     strings.TrimSpace(os.Getenv("YOUTUBE_API_KEY")),
		auth:           authCfg,
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
	authHandler, err := auth.New(ctx, cfg.auth, serveAuthPage(http.StatusForbidden, deniedHTML))
	if err != nil {
		// Discovery errors are generic, but the text is not needed to act: the issuer is in the config.
		log.Print("auth setup failed: invalid configuration or identity provider unreachable")
		ln.Close()
		return 1
	}
	chatSrv := newChatServer(parseAllowedOrigins(cfg.allowedOrigins), identityNickname)
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
		Handler:           newRouter(chatSrv, gifs, playlists, authGuards(authHandler)),
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
