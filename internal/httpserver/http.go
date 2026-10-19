// Package httpserver serves MCP over Streamable HTTP, with bearer auth, a
// Host/Origin allow-list and a health endpoint.
package httpserver

import (
	"bytes"
	"crypto/subtle"
	"encoding/json"
	"errors"
	"io"
	"log"
	"net"
	"net/http"
	"net/url"
	"regexp"
	"slices"
	"strconv"
	"strings"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/mattoddie/octopus-mcp/internal/config"
)

// MaxBodyBytes is the largest request body accepted on /mcp. Tool arguments are small.
const MaxBodyBytes = 4 << 20

func writeJSON(w http.ResponseWriter, status int, body any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(body)
}

func rpcError(w http.ResponseWriter, status int, message string) {
	writeJSON(w, status, map[string]any{"jsonrpc": "2.0", "error": map[string]any{"code": -32000, "message": message}, "id": nil})
}

func tokenMatches(header, token string) bool {
	presented, ok := strings.CutPrefix(header, "Bearer ")
	if !ok {
		presented, ok = strings.CutPrefix(header, "bearer ")
	}
	return ok && subtle.ConstantTimeCompare([]byte(strings.TrimSpace(presented)), []byte(token)) == 1
}

var portSuffix = regexp.MustCompile(`:\d+$`)

// hostAllowed reports whether a Host header is in the allow-list. An entry
// without a port matches any port, since the server's own port varies behind
// Docker; an entry with a port matches only that port.
func hostAllowed(hostHeader string, allowed []string) bool {
	host := strings.ToLower(hostHeader)
	return slices.Contains(allowed, host) || slices.Contains(allowed, portSuffix.ReplaceAllString(host, ""))
}

// originAllowed reports whether an Origin header is in the allow-list. An entry
// without a port matches only the scheme's default port (80 or 443); a page on
// another port needs a host:port entry. "null", empty and malformed origins are rejected.
func originAllowed(origin string, allowed []string) bool {
	u, err := url.Parse(origin)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" || u.Opaque != "" || u.User != nil {
		return false
	}
	hostname := strings.ToLower(u.Hostname())
	if strings.Contains(hostname, ":") {
		hostname = "[" + hostname + "]"
	}
	defaultPort := "80"
	if u.Scheme == "https" {
		defaultPort = "443"
	}
	port := u.Port()
	if port == "" || port == defaultPort {
		return slices.Contains(allowed, hostname) || slices.Contains(allowed, hostname+":"+defaultPort)
	}
	return slices.Contains(allowed, hostname+":"+port)
}

// Handler serves /mcp (stateless Streamable HTTP, so the container can be restarted or scaled freely) and /health.
func Handler(cfg config.HTTP, server *mcp.Server) http.Handler {
	mcpHandler := mcp.NewStreamableHTTPHandler(func(*http.Request) *mcp.Server { return server }, &mcp.StreamableHTTPOptions{
		Stateless:                  true,
		JSONResponse:               true,
		DisableLocalhostProtection: len(cfg.AllowedHosts) > 0, // MCP_ALLOWED_HOSTS replaces the built-in localhost check
		MaxRequestBodyBytes:        MaxBodyBytes,
	})
	mux := http.NewServeMux()
	health := func(w http.ResponseWriter, _ *http.Request) {
		writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
	}
	mux.HandleFunc("/health", health)
	mux.HandleFunc("/healthz", health)
	mux.HandleFunc("/mcp", func(w http.ResponseWriter, r *http.Request) {
		if len(cfg.AllowedHosts) > 0 {
			if !hostAllowed(r.Host, cfg.AllowedHosts) {
				rpcError(w, http.StatusForbidden, "Host "+strings.ToLower(r.Host)+" is not allowed")
				return
			}
			// Browsers send Origin on cross-site requests; non-browser clients usually don't.
			// A present but empty or "null" Origin is rejected too.
			if origins, ok := r.Header["Origin"]; ok {
				if len(origins) != 1 || !originAllowed(origins[0], cfg.AllowedHosts) {
					rpcError(w, http.StatusForbidden, "Origin "+strings.Join(origins, ", ")+" is not allowed")
					return
				}
			}
		}
		if cfg.AuthToken != "" && !tokenMatches(r.Header.Get("Authorization"), cfg.AuthToken) {
			w.Header().Set("WWW-Authenticate", "Bearer")
			rpcError(w, http.StatusUnauthorized, "Unauthorized")
			return
		}
		if r.Method != http.MethodPost {
			// Stateless mode: no server-initiated SSE stream and no sessions to delete.
			w.Header().Set("Allow", "POST")
			rpcError(w, http.StatusMethodNotAllowed, "Method not allowed")
			return
		}
		body, err := io.ReadAll(io.LimitReader(r.Body, MaxBodyBytes+1))
		if err != nil {
			rpcError(w, http.StatusBadRequest, "Could not read the request body")
			return
		}
		if len(body) > MaxBodyBytes {
			rpcError(w, http.StatusRequestEntityTooLarge, "Request body too large")
			return
		}
		if !json.Valid(body) {
			rpcError(w, http.StatusBadRequest, "Invalid JSON body")
			return
		}
		r.Body = io.NopCloser(bytes.NewReader(body))
		r.ContentLength = int64(len(body))
		mcpHandler.ServeHTTP(w, r)
	})
	mux.HandleFunc("/", func(w http.ResponseWriter, _ *http.Request) {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "Not found. The MCP endpoint is /mcp"})
	})
	return mux
}

// Addr is the listen address.
func Addr(cfg config.HTTP) string {
	return net.JoinHostPort(cfg.Host, strconv.Itoa(cfg.Port))
}

// Serve listens until the server is shut down.
func Serve(srv *http.Server, cfg config.HTTP) error {
	auth := " (bearer auth enabled)"
	if cfg.AuthToken == "" {
		auth = ""
		log.Printf("octopus-mcp: WARNING: serving /mcp without authentication (MCP_ALLOW_UNAUTHENTICATED=true): anyone who can reach the port can use every enabled tool")
	}
	log.Printf("octopus-mcp listening on http://%s/mcp%s", srv.Addr, auth)
	if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
		return err
	}
	return nil
}
