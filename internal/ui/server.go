package ui

import (
	"context"
	"embed"
	"fmt"
	"html/template"
	"net"
	"net/http"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/gorilla/websocket"
	"github.com/sirupsen/logrus"
)

//go:embed static
var staticFiles embed.FS

type Message struct {
	Direction string `json:"direction"`
	Content   string `json:"content"`
	Timestamp string `json:"timestamp"`
	Duration  int64  `json:"duration"`
}

type Server struct {
	logger     *logrus.Logger
	upgrader   websocket.Upgrader
	clients    map[*websocket.Conn]bool
	clientsMux sync.Mutex
	server     *http.Server
}

func NewServer(logger *logrus.Logger) *Server {
	return &Server{
		logger: logger,
		upgrader: websocket.Upgrader{
			ReadBufferSize:  1024,
			WriteBufferSize: 1024,
			CheckOrigin: func(r *http.Request) bool {
				// Only allow localhost connections
				host, _, err := net.SplitHostPort(r.Host)
				if err != nil {
					return false
				}
				return host == "localhost" || host == "127.0.0.1"
			},
		},
		clients: make(map[*websocket.Conn]bool),
	}
}

func (s *Server) Start(port int) error {
	// Serve static files directly from embedded filesystem
	http.HandleFunc("/static/", func(w http.ResponseWriter, r *http.Request) {
		path := strings.TrimPrefix(r.URL.Path, "/static/")
		data, err := staticFiles.ReadFile("static/" + path)
		if err != nil {
			http.NotFound(w, r)
			return
		}

		// Set content type based on file extension
		switch filepath.Ext(path) {
		case ".js":
			w.Header().Set("Content-Type", "application/javascript")
		case ".css":
			w.Header().Set("Content-Type", "text/css")
		}

		w.Write(data)
	})

	http.HandleFunc("/", s.handleHome)
	http.HandleFunc("/ws", s.handleWebSocket)

	listener, err := net.Listen("tcp", fmt.Sprintf("localhost:%d", port))
	if err != nil {
		return fmt.Errorf("error creating listener: %v", err)
	}

	addr := listener.Addr().(*net.TCPAddr)
	s.logger.Infof("starting debugging UI server on http://%s", addr.String())

	s.server = &http.Server{
		Addr:         addr.String(),
		ReadTimeout:  5 * time.Second,
		WriteTimeout: 10 * time.Second,
		IdleTimeout:  120 * time.Second,
	}

	return s.server.Serve(listener)
}

func (s *Server) handleHome(w http.ResponseWriter, r *http.Request) {
	// Only allow GET requests.
	if r.Method != http.MethodGet {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	tmpl := `
<!DOCTYPE html>
<html>
<head>
    <title>lspwatch debug UI</title>
    <link rel="stylesheet" href="/static/css/styles.css">
</head>
<body>
    <div class="container">
        <h1>LSP Messages</h1>
        <div id="messages"></div>
    </div>
    <script src="/static/js/app.js"></script>
</body>
</html>`

	t, err := template.New("home").Parse(tmpl)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	if err := t.Execute(w, nil); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
	}
}

func (s *Server) handleWebSocket(w http.ResponseWriter, r *http.Request) {
	// Only allow GET requests.
	if r.Method != http.MethodGet {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	conn, err := s.upgrader.Upgrade(w, r, nil)
	if err != nil {
		s.logger.Errorf("Error upgrading to WebSocket: %v", err)
		return
	}

	s.clientsMux.Lock()
	s.clients[conn] = true
	s.clientsMux.Unlock()

	defer func() {
		s.clientsMux.Lock()
		delete(s.clients, conn)
		s.clientsMux.Unlock()
		conn.Close()
	}()

	// Keep the connection alive.
	for {
		_, _, err := conn.ReadMessage()
		if err != nil {
			break
		}
	}
}

func (s *Server) BroadcastMessage(msg Message) {
	s.clientsMux.Lock()
	defer s.clientsMux.Unlock()

	for client := range s.clients {
		err := client.WriteJSON(msg)
		if err != nil {
			s.logger.Errorf("Error broadcasting message: %v", err)
			client.Close()
			delete(s.clients, client)
		}
	}
}

// Shutdown gracefully shuts down the UI server and closes all WebSocket connections.
func (s *Server) Shutdown() error {
	if s.server == nil {
		return nil
	}

	// Close all WebSocket connections.
	s.clientsMux.Lock()
	for client := range s.clients {
		client.Close()
		delete(s.clients, client)
	}
	s.clientsMux.Unlock()

	// Create a context with timeout for graceful shutdown.
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	return s.server.Shutdown(ctx)
}
