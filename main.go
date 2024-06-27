package main

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"log"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/brianvoe/gofakeit/v6"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"golang.org/x/sys/unix"
)

const (
	deploymentDir     = "./deployment-logs"
	logIdleTimeout    = 60
	readHeaderTimeout = 5 * time.Second
)

var (
	clients    = make(map[string]*gin.Context)
	clientsMtx sync.Mutex
)

func main() {
	if err := os.MkdirAll(deploymentDir, 0755); err != nil {
		log.Fatalf("failed to create deployment directory: %v", err)
	}

	r := gin.Default()
	r.GET("/", handleIndex)
	r.POST("/deployment", handleDeployment)
	r.GET("/logs/:id", streamLogs)
	r.POST("/disconnect", handleDisconnect)
	r.POST("/ping", handlePing)

	httpServer := &http.Server{
		Addr:              ":8080",
		ReadHeaderTimeout: readHeaderTimeout,
		Handler:           r,
	}

	go func() {
		log.Printf("listening on %s\n", httpServer.Addr)
		if err := httpServer.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			fmt.Fprintf(os.Stderr, "error listening and serving: %s\n", err)
		}
	}()

	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()

	<-ctx.Done()
	log.Println("Shutting down server...")

	notifyClientsOfTermination()
	cleanupDirectory()

	shutdownCtx, cancel := context.WithTimeout(context.Background(), 1*time.Minute)
	defer cancel()

	if err := httpServer.Shutdown(shutdownCtx); err != nil {
		log.Fatalf("error during server shutdown : %v", err)
	}

	log.Println("Server exited properly")
}

func handleIndex(c *gin.Context) {
	content, err := os.ReadFile("index.html")
	if err != nil {
		c.String(http.StatusInternalServerError, "error loading index file")
		return
	}

	c.Data(http.StatusOK, "text/html; charset=utf-8", content)
}

func handleDeployment(c *gin.Context) {
	deploymentID := uuid.New().String()
	filePath := filepath.Join(deploymentDir, deploymentID+".txt")

	file, err := os.Create(filePath)
	if err != nil {
		log.Fatalf("error creating new deployment: %s", err)
	}

	defer func(file *os.File) {
		err := file.Close()
		if err != nil {
			log.Fatalf("error deleting deployment: %s, error: %s", deploymentID, err)
		}
	}(file)

	go generateSentences(filePath)

	c.JSON(http.StatusOK, gin.H{
		"deployment_id": deploymentID,
	})
}

func generateSentences(filePath string) {
	file, err := os.OpenFile(filePath, os.O_APPEND|os.O_WRONLY, 0644)
	if err != nil {
		log.Printf("error occurred during deployment: %s", err)
		return
	}
	defer file.Close()

	for i := 0; i < 60; i++ {
		timeStamp := time.Now().Format(time.RFC3339Nano)
		sentence := gofakeit.Sentence(30)
		logEntry := fmt.Sprintf("%s: %s\n", timeStamp, sentence)
		if _, err := file.WriteString(logEntry); err != nil {
			log.Printf("error writing to file %s: %v", filePath, err)
			return
		}
		time.Sleep(300 * time.Millisecond)
	}
}

func streamLogs(c *gin.Context) {
	deploymentID := c.Param("id")
	filePath := filepath.Join(deploymentDir, deploymentID+".txt")
	file, err := os.Open(filePath)
	if err != nil {
		log.Printf("error opening the file for streaming logs, deployment-id: %s, error: %s", deploymentID, err)
		return
	}

	defer func(file *os.File) {
		err := file.Close()
		if err != nil {
			fmt.Printf("error closing file for streaming logs, deployment-id: %s, error: %s", deploymentID, err)
		}
	}(file)

	clientID := uuid.New().String()
	setupResponseHeaders(c, clientID)
	addClient(clientID, c)
	defer removeClient(clientID)

	reader := bufio.NewReader(file)

	handleLogStreaming(c, reader, clientID)
}

func setupResponseHeaders(c *gin.Context, clientID string) {
	c.Header("Content-Type", "text/event-stream")
	c.Header("Cache-Control", "no-cache")
	c.Header("Connection", "keep-alive")
	c.Header("X-Client-ID", clientID)
	c.Writer.WriteHeaderNow() // Disable buffering
}

func handleLogStreaming(c *gin.Context, reader *bufio.Reader, clientID string) {
	eofCount := 0
	for {
		if checkForInactivity(c, clientID, eofCount) {
			break
		}

		line, err := readLogLine(reader)
		if err != nil {
			if err.Error() == "EOF" {
				time.Sleep(400 * time.Millisecond)
				eofCount++
				continue
			}
			log.Printf("error reading file for streaming logs: %s", err)
			continue
		}

		if !sendLogLine(c, line, clientID) {
			return
		}
		eofCount = 0
	}
}

func checkForInactivity(c *gin.Context, clientID string, eofCount int) bool {
	if eofCount == logIdleTimeout {
		_, err := fmt.Fprintf(c.Writer, "event: inactivity\ndata: closing connection due to inactivity.\n\n")
		if err != nil {
			log.Printf("unable to notify client %s: %v", clientID, err)
		} else {
			c.Writer.Flush()
		}

		return true
	}

	return false
}

func readLogLine(reader *bufio.Reader) (string, error) {
	line, err := reader.ReadString('\n')
	line = strings.TrimRight(line, "\n")
	return line, err
}

func sendLogLine(c *gin.Context, line, clientID string) bool {
	// Write the log line to the client in SSE format
	_, err := fmt.Fprintf(c.Writer, "data: %s\n\n", line)
	if err != nil {
		if isEPIPE(err) {
			log.Printf("client connection closed: %s", err)
		} else if errors.Is(err, context.Canceled) {
			log.Printf("Request canceled: %s\n", clientID)
		} else {
			log.Printf("Error sending logs to client %s: %v\n", clientID, err)
		}

		return false // Indicate failure to send
	}

	// Flush the data to the client
	c.Writer.Flush()

	return true
}

func isEPIPE(err error) bool {
	var sErr *os.SyscallError
	return errors.As(err, &sErr) && errors.Is(sErr.Err, unix.EPIPE)
}

func cleanupDirectory() {
	log.Println("Cleaning up directory...")
	if err := os.RemoveAll(deploymentDir); err != nil {
		log.Printf("error removing directory %s: %v", deploymentDir, err)
	} else {
		log.Println("Cleanup complete.")
	}
}

func notifyClientsOfTermination() {
	clientsMtx.Lock()
	defer clientsMtx.Unlock()
	for id, c := range clients {
		_, err := fmt.Fprintf(c.Writer, "event: termination\ndata: Server is shutting down.\n\n")
		if err != nil {
			log.Printf("unable to notify client %s: %v", id, err)
		} else {
			c.Writer.Flush()
		}
	}
}

func handleDisconnect(c *gin.Context) {
	clientID := c.GetHeader("X-Client-ID")
	if clientID == "" {
		c.String(http.StatusBadRequest, "Missing client ID")
		return
	}

	removeClient(clientID)
	c.String(http.StatusOK, "Disconnected")
}

func addClient(id string, c *gin.Context) {
	clientsMtx.Lock()
	defer clientsMtx.Unlock()
	clients[id] = c
}

func removeClient(id string) {
	clientsMtx.Lock()
	defer clientsMtx.Unlock()
	delete(clients, id)
}

func handlePing(c *gin.Context) {
	clientID := c.GetHeader("X-Client-ID")
	if clientID != "" {
		// Optionally log or update client activity here
		c.String(http.StatusOK, "Pong")
	} else {
		c.String(http.StatusBadRequest, "Missing client ID")
	}
}
