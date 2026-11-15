package main

import (
	"context"
	"fmt"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"parser/internal/ai"
	"parser/internal/handler"
	"parser/internal/parser"
	"parser/internal/repository"
	"parser/internal/service"
)

func main() {
	dbURL := os.Getenv("DB_URL")
	port := os.Getenv("PORT")
	aiURL := os.Getenv("AI_URL")
	if dbURL == "" || port == "" || aiURL == "" {
		fmt.Fprintln(os.Stderr, "DB_URL, PORT and AI_URL must be set")
		os.Exit(1)
	}

	repo, err := repository.NewRepo(dbURL)
	if err != nil {
		fmt.Fprintln(os.Stderr, "DB connection error:", err)
		os.Exit(1)
	}
	defer repo.CloseDB()

	aiClient := ai.NewClient(aiURL)
	parseService := service.NewParserService(parser.NewParser(), repo)
	searchService := service.NewSearchService(repo, aiClient)
	apiHandler := handler.NewHandler(parseService, searchService)

	mux := http.NewServeMux()
	mux.Handle("POST /parse", apiHandler.Chain(http.HandlerFunc(apiHandler.Parser),
		apiHandler.RecoverMiddleware, apiHandler.LoggingMiddleware))
	mux.Handle("POST /chat", apiHandler.Chain(http.HandlerFunc(apiHandler.Chat),
		apiHandler.RecoverMiddleware, apiHandler.LoggingMiddleware))

	server := &http.Server{
		Addr:              ":" + port,
		Handler:           mux,
		ReadHeaderTimeout: 10 * time.Second,
		ReadTimeout:       30 * time.Minute,
		WriteTimeout:      30 * time.Minute,
		IdleTimeout:       time.Hour,
	}

	done := make(chan os.Signal, 1)
	signal.Notify(done, os.Interrupt, syscall.SIGINT, syscall.SIGTERM)
	defer signal.Stop(done)

	serverErr := make(chan error, 1)
	go func() {
		if err := server.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			serverErr <- err
		}
	}()

	select {
	case <-done:
		fmt.Println("The server is stopping...")
	case err := <-serverErr:
		fmt.Fprintln(os.Stderr, "HTTP server error:", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := server.Shutdown(ctx); err != nil {
		fmt.Fprintln(os.Stderr, "shutdown error:", err)
	}
}
