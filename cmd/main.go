package main

import (
	"context"
	"fmt"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"parser/internal/handler"
	"parser/internal/parser"
	"parser/internal/repository"
	"parser/internal/service"
)

func main() {

	dbUrl := os.Getenv("DB_URL")
	port := os.Getenv("PORT")

	mux := &http.ServeMux{}

	server := &http.Server{
		Addr:         ":" + port,
		Handler:      mux,
		ReadTimeout:  30 * time.Second,
		WriteTimeout: 2 * time.Minute,
		IdleTimeout:  5 * time.Minute,
	}

	parser := parser.NewParser()
	repo, err := repository.NewRepo(dbUrl)
	if err != nil {
		return
	}
	defer repo.CloseDB()
	service := service.NewService(parser, repo)
	handler := handler.NewHandler(service)

	mux.Handle("POST /parse", handler.Chain(http.HandlerFunc(handler.Parser),
		handler.RecoverMiddleware,
		handler.LoggingMiddleware))

	done := make(chan os.Signal, 1)
	signal.Notify(done, os.Interrupt, syscall.SIGINT, syscall.SIGTERM)

	go func() {
		fmt.Println("http://localhost:8080")
		if err := server.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			panic(err)
		}
	}()

	<-done
	fmt.Println("The server is stopping...")

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	if err := server.Shutdown(ctx); err != nil {
		fmt.Println("shutdown error", err)
	} else {
		fmt.Println("server stop")
	}

}
