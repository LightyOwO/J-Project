package main

import (
	"context"
	"j-project/src/utils/ai"
	"j-project/src/utils/tts"
	"log"
	"net/http"

	"github.com/gin-gonic/gin"
	"github.com/gorilla/websocket"
	"github.com/joho/godotenv"
)

var upgrader = websocket.Upgrader{
	ReadBufferSize:  1024,
	WriteBufferSize: 1024,
	CheckOrigin:     func(r *http.Request) bool { return true },
}

func main() {
	// Load .env file if present
	_ = godotenv.Load()

	// Demonstrate prompting the AI (which may invoke web search internally)
	ctx := context.Background()
	prompt := "What are some common concurrency patterns in Go?"
	log.Printf("Prompting AI (ollama): %s", prompt)
	aiResponse := ""
	err := ai.Stream(ctx, "ollama", prompt, func(chunk string) {
		log.Printf("AI chunk: %s", chunk)
		aiResponse += chunk + " "
	})
	if err != nil {
		log.Printf("AI error: %v", err)
	} else {
		log.Printf("AI full response: %s", aiResponse)
	}

	ginrouter := gin.Default()

	ginrouter.GET("/health", func(c *gin.Context) {
		c.Data(http.StatusOK, "text/plain", []byte("OK"))
	})

	// WebSocket endpoint for live AI comms. Client should send a JSON or plain text prompt.
	ginrouter.GET("/ws/ai", func(c *gin.Context) {
		conn, err := upgrader.Upgrade(c.Writer, c.Request, nil)
		if err != nil {
			c.Error(err)
			return
		}
		defer conn.Close()

		// read provider from the initial HTTP query parameters
		provider := c.Query("provider") // e.g. "jetify", "anthropic", "ollama"

		for {
			// Read message (blocking until client sends)
			_, msg, err := conn.ReadMessage()
			if err != nil {
				log.Printf("ws read error: %v", err)
				return
			}

			prompt := string(msg)
			log.Printf("ws: received prompt (provider=%s): %s", provider, prompt)

			// create a cancellable context so the handler can stop streaming on write errors
			ctx, cancel := context.WithCancel(context.Background())

			// handler called by ai.Stream for every chunk
			handler := func(chunk string) {
				// attempt to write; on failure cancel the stream
				if err := conn.WriteMessage(websocket.TextMessage, []byte(chunk)); err != nil {
					log.Printf("ws write error: %v", err)
					cancel()
					return
				}
				// non-blocking TTS for each chunk
				tts.Speak("espeak", chunk)
			}

			// call provider stream (this will block until provider completes or ctx is cancelled)
			if err := ai.Stream(ctx, provider, prompt, handler); err != nil {
				log.Printf("ai stream error: %v", err)
				// try to inform client about the error, then continue
				_ = conn.WriteMessage(websocket.TextMessage, []byte("__error__: "+err.Error()))
				cancel()
				continue
			}

			// indicate stream end
			if err := conn.WriteMessage(websocket.TextMessage, []byte("__end__")); err != nil {
				log.Printf("ws write error on end marker: %v", err)
				return
			}

			cancel()
		}
	})

	log.Println("starting server on :8080")
	ginrouter.Run(":8080")
}
