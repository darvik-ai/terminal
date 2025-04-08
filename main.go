package main

import (
	"encoding/json"
	"log"
	"net/http"
	"os"
	"os/exec"
	"time"

	"github.com/creack/pty"
	"github.com/gorilla/websocket"
	// We don't directly use client-go here, but ensure it's in go.mod
	// if you were to add direct API calls later.
	// _ "k8s.io/client-go/rest"
)

var upgrader = websocket.Upgrader{
	ReadBufferSize:  1024,
	WriteBufferSize: 1024,
	CheckOrigin:     func(r *http.Request) bool { return true }, // Allow all origins for simplicity
}

// PtyPayload defines the structure for messages exchanged over WebSocket
type PtyPayload struct {
	Type string `json:"type"` // "input", "resize"
	Data string `json:"data,omitempty"`
	Rows uint16 `json:"rows,omitempty"`
	Cols uint16 `json:"cols,omitempty"`
}

func handleWebSocket(w http.ResponseWriter, r *http.Request) {
	log.Println("New WebSocket connection attempt")
	conn, err := upgrader.Upgrade(w, r, nil)
	if err != nil {
		log.Printf("Failed to upgrade connection: %v", err)
		return
	}
	defer conn.Close()
	log.Println("WebSocket connection established")

	// --- Start Shell using PTY ---
	// Start the command with a PTY. Use bash if available, otherwise sh.
	shell := os.Getenv("SHELL")
	if shell == "" {
		shell = "sh" // Default shell
		if _, err := exec.LookPath("bash"); err == nil {
			shell = "bash" // Prefer bash if available
		}
	}
	log.Printf("Starting PTY with shell: %s", shell)
	cmd := exec.Command(shell)
	// Set TERM environment variable for compatibility (like colors)
	cmd.Env = append(os.Environ(), "TERM=xterm")

	ptmx, err := pty.Start(cmd)
	if err != nil {
		log.Printf("Failed to start pty: %v", err)
		conn.WriteMessage(websocket.TextMessage, []byte("Error starting terminal: "+err.Error()))
		return
	}
	defer func() {
		_ = ptmx.Close()
		_ = cmd.Process.Kill() // Ensure process is killed
		log.Println("PTY closed and process killed")
	}()
	log.Println("PTY started successfully")


	// --- Goroutine to read from PTY and write to WebSocket ---
	go func() {
		buffer := make([]byte, 4096) // Buffer for reading PTY output
		for {
			n, err := ptmx.Read(buffer)
			if err != nil {
				log.Printf("Failed to read from PTY: %v", err)
				// Send close message or error to client? Best effort for now.
				conn.WriteMessage(websocket.CloseMessage, websocket.FormatCloseMessage(websocket.CloseNormalClosure, "PTY read error"))
				return
			}
			if n > 0 {
				// log.Printf("PTY -> WS: %d bytes", n) // Debug logging
				err = conn.WriteMessage(websocket.BinaryMessage, buffer[:n])
				if err != nil {
					log.Printf("Failed to write to WebSocket: %v", err)
					return // Stop goroutine if WebSocket write fails
				}
			}
		}
	}()

	// --- Read loop for WebSocket messages (Input, Resize) ---
	for {
		messageType, p, err := conn.ReadMessage()
		if err != nil {
			log.Printf("WebSocket read error: %v", err)
			break // Exit loop on read error (client disconnected, etc.)
		}

		// Expecting JSON messages
		if messageType != websocket.TextMessage {
			log.Printf("Received non-text message type: %d", messageType)
			continue
		}

		var payload PtyPayload
		err = json.Unmarshal(p, &payload)
		if err != nil {
			log.Printf("Failed to unmarshal JSON payload: %v, Data: %s", err, string(p))
			continue
		}

		switch payload.Type {
		case "input":
			// log.Printf("WS -> PTY: %s", payload.Data) // Debug logging
			_, err = ptmx.Write([]byte(payload.Data))
			if err != nil {
				log.Printf("Failed to write to PTY: %v", err)
				// Consider sending an error back to the client
				continue // Don't break loop on write error, maybe temporary
			}
		case "resize":
			log.Printf("Resizing PTY to Rows: %d, Cols: %d", payload.Rows, payload.Cols)
			err = pty.Setsize(ptmx, &pty.Winsize{Rows: payload.Rows, Cols: payload.Cols})
			if err != nil {
				log.Printf("Failed to resize PTY: %v", err)
				// Inform client? Maybe not critical.
			}
		default:
			log.Printf("Unknown payload type received: %s", payload.Type)
		}
	}

	log.Println("WebSocket connection closed")
}

func main() {
	// Serve static files (index.html)
	fs := http.FileServer(http.Dir("./static"))
	http.Handle("/", fs)

	// Handle WebSocket connections
	http.HandleFunc("/ws", handleWebSocket)

	port := os.Getenv("PORT")
	if port == "" {
		port = "8080" // Default port
	}

	log.Printf("Starting web server on :%s", port)
	log.Printf("Serving static files from ./static")
	log.Printf("WebSocket endpoint at /ws")

	// Check if kubectl is available in the PATH
	if _, err := exec.LookPath("kubectl"); err != nil {
		log.Println("WARNING: 'kubectl' command not found in PATH. Kubernetes commands will fail.")
	} else {
		log.Println("'kubectl' command found in PATH.")
		// Optional: Verify in-cluster config detection works (requires running inside k8s)
		// _, err := rest.InClusterConfig()
		// if err != nil {
		//	 log.Printf("WARNING: Could not detect in-cluster Kubernetes config: %v", err)
		// } else {
		//	 log.Println("Successfully detected in-cluster Kubernetes config.")
		// }
	}


	srv := &http.Server{
		Addr:         ":" + port,
		ReadTimeout:  10 * time.Second, // Basic timeouts
		WriteTimeout: 10 * time.Second,
		IdleTimeout:  60 * time.Second,
	}

	if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
		log.Fatalf("ListenAndServe failed: %v", err)
	}
}