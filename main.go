// My Apps demo: a small web page that customers deploy through My Apps to see
// how it works. It shows their settings, writes a log line on request and
// changes colour between versions, so an update is visible.
//
// Standard library only. Everything the page needs is embedded in the binary.
package main

import (
	"context"
	"embed"
	"encoding/json"
	"errors"
	"io/fs"
	"log/slog"
	"net"
	"net/http"
	"os"
	"os/signal"
	"regexp"
	"strings"
	"sync"
	"syscall"
	"time"
	"unicode"
)

// version is set at build time: -ldflags "-X main.version=1.1.0".
var version = "dev"

//go:embed web
var webFiles embed.FS

// release is what changes between the published versions. The code is the
// same; only the default colour and the notes differ, so a tag change in My
// Apps shows up on the page at a glance.
type release struct {
	Color string   `json:"color"`
	Notes []string `json:"notes"`
}

var releases = map[string]release{
	"1.0.0": {Color: "blue", Notes: []string{"The first version of the demo."}},
	"1.1.0": {Color: "green", Notes: []string{
		"The page is green by default.",
		"This list of changes is new.",
	}},
}

func currentRelease() release {
	if r, ok := releases[version]; ok {
		return r
	}
	return releases["1.0.0"]
}

var namedColors = map[string]string{
	"blue":   "#2563eb",
	"green":  "#16a34a",
	"purple": "#7c3aed",
	"orange": "#ea580c",
	"pink":   "#db2777",
	"red":    "#dc2626",
	"teal":   "#0d9488",
}

var hexColor = regexp.MustCompile(`^#[0-9a-fA-F]{6}$`)

var confettiStyles = []string{"classic", "stars", "snow", "hearts", "party", "off"}

// setting is one environment variable the page reads, as the customer sees it.
type setting struct {
	Name     string `json:"name"`
	Value    string `json:"value"`
	IsSet    bool   `json:"isSet"`
	Accepted bool   `json:"accepted"`
	Used     string `json:"used"`
	Effect   string `json:"effect"`
	Options  string `json:"options"`
}

type settings struct {
	List     []setting `json:"list"`
	Color    string    `json:"color"`
	Confetti string    `json:"confetti"`
	Greeting string    `json:"greeting"`
}

func readSettings() settings {
	var s settings

	defaultColor := currentRelease().Color
	color := readSetting("PAGE_COLOR", "The colour of this page.",
		"blue, green, purple, orange, pink, red, teal, or a code like #0d9488",
		defaultColor, func(v string) (string, bool) {
			v = strings.ToLower(v)
			if _, ok := namedColors[v]; ok {
				return v, true
			}
			return v, hexColor.MatchString(v)
		})
	s.List = append(s.List, color)
	s.Color = color.Used
	if hex, ok := namedColors[s.Color]; ok {
		s.Color = hex
	}

	confetti := readSetting("CONFETTI_STYLE", "The confetti when the page opens.",
		strings.Join(confettiStyles, ", "), "classic", func(v string) (string, bool) {
			v = strings.ToLower(v)
			for _, style := range confettiStyles {
				if v == style {
					return v, true
				}
			}
			return v, false
		})
	s.List = append(s.List, confetti)
	s.Confetti = confetti.Used

	greeting := readSetting("GREETING", "The welcome line under the heading.",
		"any text", "Welcome to your first application.", func(v string) (string, bool) {
			return clean(v, 120), true
		})
	s.List = append(s.List, greeting)
	s.Greeting = greeting.Used

	return s
}

// readSetting reads one variable. A value that is set but not accepted falls
// back to the default, and the page says so instead of failing.
func readSetting(name, effect, options, fallback string, accept func(string) (string, bool)) setting {
	raw, isSet := os.LookupEnv(name)
	raw = strings.TrimSpace(raw)
	s := setting{Name: name, Value: raw, IsSet: isSet && raw != "", Effect: effect, Options: options, Used: fallback}
	if !s.IsSet {
		return s
	}
	if value, ok := accept(raw); ok {
		s.Accepted = true
		s.Used = value
	}
	return s
}

// clean keeps printable characters only and caps the length, so text from a
// setting or a visitor cannot break a log line.
func clean(text string, max int) string {
	var b strings.Builder
	n := 0
	for _, r := range strings.TrimSpace(text) {
		if n == max {
			break
		}
		if unicode.IsPrint(r) {
			b.WriteRune(r)
			n++
		}
	}
	return b.String()
}

type info struct {
	Version      string    `json:"version"`
	Notes        []string  `json:"notes"`
	Versions     []string  `json:"versions"`
	Address      string    `json:"address"`
	Application  string    `json:"application"`
	Organisation string    `json:"organisation"`
	StartedAt    time.Time `json:"startedAt"`
	Settings     settings  `json:"settings"`
}

var startedAt = time.Now().UTC()

func handleInfo(cfg settings) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		host := r.Host
		if h, _, err := net.SplitHostPort(host); err == nil {
			host = h
		}
		scheme := "http"
		if r.TLS != nil || r.Header.Get("X-Forwarded-Proto") == "https" {
			scheme = "https"
		}
		// My Apps addresses read <application>.<organisation>.<domain>.
		var application, organisation string
		if labels := strings.Split(host, "."); len(labels) >= 4 {
			application, organisation = labels[0], labels[1]
		}
		writeJSON(w, http.StatusOK, info{
			Version:      version,
			Notes:        currentRelease().Notes,
			Versions:     []string{"1.0.0", "1.1.0"},
			Address:      scheme + "://" + r.Host,
			Application:  application,
			Organisation: organisation,
			StartedAt:    startedAt,
			Settings:     cfg,
		})
	}
}

// limiter allows a burst of log lines and then about one every two seconds.
// The page is public, so anyone with the address can post.
type limiter struct {
	mu     sync.Mutex
	tokens float64
	last   time.Time
}

func (l *limiter) allow() bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	now := time.Now()
	l.tokens = min(10, l.tokens+now.Sub(l.last).Seconds()/2)
	l.last = now
	if l.tokens < 1 {
		return false
	}
	l.tokens--
	return true
}

func handleLog(logger *slog.Logger) http.HandlerFunc {
	limit := &limiter{tokens: 10, last: time.Now()}
	return func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			Message string `json:"message"`
		}
		if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 2048)).Decode(&body); err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "We could not read that message."})
			return
		}
		message := clean(body.Message, 200)
		if message == "" {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "Type a message first."})
			return
		}
		if !limit.allow() {
			writeJSON(w, http.StatusTooManyRequests, map[string]string{"error": "That is a lot of messages. Wait a few seconds and try again."})
			return
		}
		logger.Info("Message from the demo page", "message", message)
		writeJSON(w, http.StatusOK, map[string]string{"message": message})
	}
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

func securityHeaders(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		h := w.Header()
		h.Set("Content-Security-Policy", "default-src 'self'; img-src 'self' data:; frame-ancestors 'none'; base-uri 'none'; form-action 'none'")
		h.Set("X-Content-Type-Options", "nosniff")
		h.Set("Referrer-Policy", "no-referrer")
		// Check back on every load, so a browser never shows the previous
		// version's page after an update.
		h.Set("Cache-Control", "no-cache")
		next.ServeHTTP(w, r)
	})
}

func main() {
	logger := slog.New(slog.NewJSONHandler(os.Stdout, nil)).With("version", version)

	port := os.Getenv("PORT")
	if port == "" {
		port = "8080"
	}

	cfg := readSettings()
	for _, s := range cfg.List {
		if s.IsSet && !s.Accepted {
			logger.Warn("Setting not recognised, using the default", "setting", s.Name, "value", s.Value, "default", s.Used)
		}
	}

	web, err := fs.Sub(webFiles, "web")
	if err != nil {
		logger.Error("Embedded page missing", "error", err)
		os.Exit(1)
	}

	mux := http.NewServeMux()
	mux.Handle("GET /", http.FileServerFS(web))
	mux.HandleFunc("GET /api/info", handleInfo(cfg))
	mux.HandleFunc("POST /api/log", handleLog(logger))
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	})

	server := &http.Server{
		Addr:              ":" + port,
		Handler:           securityHeaders(mux),
		ReadHeaderTimeout: 5 * time.Second,
	}

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGTERM, os.Interrupt)
	defer stop()

	go func() {
		logger.Info("My Apps demo started", "port", port, "color", cfg.Color, "confetti", cfg.Confetti)
		if err := server.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			logger.Error("Server stopped", "error", err)
			os.Exit(1)
		}
	}()

	// Finish open requests on shutdown, so an update never cuts a visitor off.
	<-ctx.Done()
	logger.Info("Stopping, a newer copy is taking over or the application was stopped")
	shutdown, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	_ = server.Shutdown(shutdown)
}
