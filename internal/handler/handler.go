package handler

import (
	"encoding/json"
	"fmt"
	"net/http"
	"time"

	"parser/internal/service"
)

type Handler struct {
	service service.Service
}

func NewHandler(service service.Service) *Handler {
	return &Handler{
		service: service,
	}
}

func (h *Handler) Parser(w http.ResponseWriter, r *http.Request) {

	serviceResults, err := h.service.Parse()
	if err != nil {
		http.Error(w, fmt.Sprint("Parser error:", err), http.StatusInternalServerError)
		w.WriteHeader(http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)

	if err := json.NewEncoder(w).Encode(serviceResults); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

}

type ResponseWriter struct {
	Status int
	http.ResponseWriter
}

func (rw *ResponseWriter) WriteHeader(status int) {
	rw.Status = status
	rw.ResponseWriter.WriteHeader(status)
}

func (h *Handler) LoggingMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {

		rw := &ResponseWriter{
			Status:         http.StatusOK,
			ResponseWriter: w,
		}

		start := time.Now()
		next.ServeHTTP(rw, r)

		fmt.Println("STATUS:", rw.Status)
		fmt.Println("Method:", r.Method)
		fmt.Println("Path:", r.URL.Path)
		fmt.Println("Time:", time.Since(start))

	})
}

func (h *Handler) RecoverMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer func() {
			if err := recover(); err != nil {
				http.Error(w, "server crash", http.StatusInternalServerError)
				return
			}
		}()
		next.ServeHTTP(w, r)
	})
}

func (h *Handler) Chain(handler http.Handler, middlewares ...func(next http.Handler) http.Handler) http.Handler {
	for i := len(middlewares) - 1; i >= 0; i-- {
		handler = middlewares[i](handler)
	}
	return handler
}
