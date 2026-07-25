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

	// Взятие переменных окружения
	dbUrl := os.Getenv("DB_URL")
	port := os.Getenv("PORT")
	aiUrl := os.Getenv("AI_URL")

	mux := &http.ServeMux{}

	server := &http.Server{
		Addr:         ":" + port,
		Handler:      mux,
		ReadTimeout:  1 * time.Minute,
		WriteTimeout: 30 * time.Minute,
		IdleTimeout:  1 * time.Hour,
	}

	// Создание зависимостей
	parser := parser.NewParser()
	repo, err := repository.NewRepo(dbUrl)
	if err != nil {
		fmt.Println("DB connection error", err)
		return
	}
	defer repo.CloseDB()
	aiChat := ai.NewClient(aiUrl)
	service := service.NewService(parser, repo, aiChat)
	handler := handler.NewHandler(service)

	mux.Handle("POST /parse", handler.Chain(http.HandlerFunc(handler.Parser),
		handler.RecoverMiddleware,
		handler.LoggingMiddleware))
	mux.Handle("POST /chat", handler.Chain(http.HandlerFunc(handler.Chat),
		handler.RecoverMiddleware,
		handler.LoggingMiddleware))

	done := make(chan os.Signal, 1)
	signal.Notify(done, os.Interrupt, syscall.SIGINT, syscall.SIGTERM)

	go func() {
		//fmt.Println("http://localhost:8080")
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
