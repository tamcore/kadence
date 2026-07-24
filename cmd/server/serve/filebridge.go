package serve

import (
	"crypto/subtle"
	"errors"
	"flag"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

const (
	defaultFileBridgeAddr     = ":8081"
	defaultFileBridgeMaxBytes = 32 << 20
	fileBridgeHealthPath      = "/healthz"
	fileBridgePathPrefix      = "/files/"
)

// FileBridgeConfig configures the file bridge's private fetch-and-delete
// endpoint. Its credentials are intended to be supplied from the process
// environment, not written to disk or logs.
type FileBridgeConfig struct {
	Root     string
	Username string
	Password string
	MaxBytes int64
}

// RunFileBridge starts the file-bridge subcommand. It reads its configuration
// from KADENCE_FILE_BRIDGE_* environment variables and allows matching flags
// to override them. This keeps the sidecar separate from the main server's
// application configuration.
func RunFileBridge() error {
	return runFileBridge(os.Args[2:], os.Getenv)
}

func runFileBridge(args []string, getenv func(string) string) error {
	maxBytes, err := fileBridgeMaxBytes(getenv("KADENCE_FILE_BRIDGE_MAX_BYTES"))
	if err != nil {
		return err
	}

	fs := flag.NewFlagSet("file-bridge", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	addr := fs.String("addr", valueOrDefault(getenv("KADENCE_FILE_BRIDGE_ADDR"), defaultFileBridgeAddr), "listen address")
	root := fs.String("root", getenv("KADENCE_FILE_BRIDGE_ROOT"), "shared FIT directory")
	username := fs.String("username", getenv("KADENCE_FILE_BRIDGE_AUTH_USER"), "basic auth username")
	password := fs.String("password", getenv("KADENCE_FILE_BRIDGE_AUTH_PASS"), "basic auth password")
	limit := fs.Int64("max-bytes", maxBytes, "maximum FIT file size")
	if err := fs.Parse(args); err != nil {
		return errors.New("invalid file bridge options")
	}

	handler, err := NewFileBridgeHandler(FileBridgeConfig{
		Root:     *root,
		Username: *username,
		Password: *password,
		MaxBytes: *limit,
	})
	if err != nil {
		return err
	}

	srv := &http.Server{
		Addr:              *addr,
		Handler:           handler,
		ReadHeaderTimeout: readHeaderTimeout,
	}
	return srv.ListenAndServe()
}

func fileBridgeMaxBytes(raw string) (int64, error) {
	if raw == "" {
		return defaultFileBridgeMaxBytes, nil
	}
	maxBytes, err := strconv.ParseInt(raw, 10, 64)
	if err != nil || maxBytes <= 0 {
		return 0, errors.New("invalid file bridge maximum size")
	}
	return maxBytes, nil
}

func valueOrDefault(value, fallback string) string {
	if value == "" {
		return fallback
	}
	return value
}

// NewFileBridgeHandler creates the authenticated fetch-and-delete endpoint.
// The resulting handler serves only GET /files/<filename>, where filename is
// a non-symlink .fit file directly under Root.
func NewFileBridgeHandler(cfg FileBridgeConfig) (http.Handler, error) {
	if cfg.Root == "" {
		return nil, errors.New("file bridge root is required")
	}
	root, err := filepath.EvalSymlinks(cfg.Root)
	if err != nil {
		return nil, errors.New("file bridge root is unavailable")
	}
	info, err := os.Stat(root)
	if err != nil || !info.IsDir() {
		return nil, errors.New("file bridge root is unavailable")
	}
	if cfg.Username == "" || cfg.Password == "" {
		return nil, errors.New("file bridge credentials are required")
	}
	if cfg.MaxBytes <= 0 {
		return nil, errors.New("file bridge maximum size must be positive")
	}
	rootHandle, err := os.OpenRoot(root)
	if err != nil {
		return nil, errors.New("file bridge root is unavailable")
	}

	return &fileBridgeHandler{
		root:     rootHandle,
		username: cfg.Username,
		password: cfg.Password,
		maxBytes: cfg.MaxBytes,
	}, nil
}

type fileBridgeHandler struct {
	root     *os.Root
	username string
	password string
	maxBytes int64
}

func (h *fileBridgeHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, http.StatusText(http.StatusMethodNotAllowed), http.StatusMethodNotAllowed)
		return
	}
	if r.URL.Path == fileBridgeHealthPath {
		w.WriteHeader(http.StatusOK)
		return
	}
	if !h.authorized(r) {
		w.Header().Set("WWW-Authenticate", `Basic realm="file-bridge"`)
		http.Error(w, http.StatusText(http.StatusUnauthorized), http.StatusUnauthorized)
		return
	}

	name, status := fileBridgeName(r.URL.Path)
	if status != http.StatusOK {
		http.Error(w, http.StatusText(status), status)
		return
	}

	h.fetchAndDelete(w, name)
}

func (h *fileBridgeHandler) authorized(r *http.Request) bool {
	username, password, ok := r.BasicAuth()
	if !ok {
		return false
	}
	usernameMatch := subtle.ConstantTimeCompare([]byte(username), []byte(h.username)) == 1
	passwordMatch := subtle.ConstantTimeCompare([]byte(password), []byte(h.password)) == 1
	return usernameMatch && passwordMatch
}

func fileBridgeName(path string) (string, int) {
	if !strings.HasPrefix(path, fileBridgePathPrefix) {
		return "", http.StatusBadRequest
	}
	name := strings.TrimPrefix(path, fileBridgePathPrefix)
	if name == "" || filepath.Base(name) != name || strings.ContainsRune(name, filepath.Separator) {
		return "", http.StatusBadRequest
	}
	if !strings.HasSuffix(name, ".fit") {
		return "", http.StatusNotFound
	}
	return name, http.StatusOK
}

func (h *fileBridgeHandler) fetchAndDelete(w http.ResponseWriter, name string) {
	pathInfo, err := h.root.Lstat(name)
	if err != nil || !pathInfo.Mode().IsRegular() {
		http.Error(w, http.StatusText(http.StatusNotFound), http.StatusNotFound)
		return
	}
	if pathInfo.Size() > h.maxBytes {
		http.Error(w, http.StatusText(http.StatusRequestEntityTooLarge), http.StatusRequestEntityTooLarge)
		return
	}

	f, err := h.root.Open(name)
	if err != nil {
		http.Error(w, http.StatusText(http.StatusNotFound), http.StatusNotFound)
		return
	}
	defer func() { _ = f.Close() }()

	openedInfo, err := f.Stat()
	if err != nil || !openedInfo.Mode().IsRegular() || !os.SameFile(pathInfo, openedInfo) {
		http.Error(w, http.StatusText(http.StatusNotFound), http.StatusNotFound)
		return
	}
	if openedInfo.Size() > h.maxBytes {
		http.Error(w, http.StatusText(http.StatusRequestEntityTooLarge), http.StatusRequestEntityTooLarge)
		return
	}

	w.Header().Set("Content-Type", "application/octet-stream")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.Header().Set("Content-Length", strconv.FormatInt(openedInfo.Size(), 10))
	w.WriteHeader(http.StatusOK)
	if _, err := io.CopyN(w, f, openedInfo.Size()); err != nil {
		return
	}

	var extra [1]byte
	if n, _ := f.Read(extra[:]); n != 0 {
		return
	}
	currentInfo, err := h.root.Lstat(name)
	if err != nil || !currentInfo.Mode().IsRegular() || !os.SameFile(openedInfo, currentInfo) {
		return
	}
	_ = h.root.Remove(name)
}
