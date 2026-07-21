package handler

import (
	"encoding/json"
	"errors"
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

type Messages struct {
	Message string `json:"message"`
}

func (h *Handler) Chat(w http.ResponseWriter, r *http.Request) {

	var message Messages
	if err := json.NewDecoder(r.Body).Decode(&message); err != nil {
		http.Error(w, fmt.Sprint("json read error", err), http.StatusBadRequest)
		w.WriteHeader(http.StatusBadRequest)
		return
	}
	defer r.Body.Close()

	resp, err := h.service.Chat(string(message.Message))
	if err != nil {
		http.Error(w, fmt.Sprint("Chat error:", err), http.StatusInternalServerError)
		w.WriteHeader(http.StatusInternalServerError)
		return
	}

	// if resp == "Need Clarification" {
	// 	w.WriteHeader(http.StatusNoContent)
	// 	w.Write([]byte(`
	// 	Уточните запрос пожалуйста используя следующие критерии:
	// 			Категория товара
	// 			Название товара
	// 			Цена товара`))
	// 	return
	// }
	if errors.Is(err, fmt.Errorf("Need Clarification")) {
		w.Header().Set("Content-type", "application/json")
		w.WriteHeader(http.StatusNoContent)
		if err := json.NewEncoder(w).Encode([]byte(`
		Уточните запрос пожалуйста используя следующие критерии:
				Категория товара
				Название товара
				Цена товара`)); err != nil {
			http.Error(w, fmt.Sprintf("JSON send error: %s", err), http.StatusInternalServerError)
			w.WriteHeader(http.StatusInternalServerError)
			return
		}
		return
	}

	w.Header().Set("Content-type", "application/json")
	w.WriteHeader(http.StatusOK)
	if err := json.NewEncoder(w).Encode(resp); err != nil {
		http.Error(w, fmt.Sprintf("JSON send error: %s", err), http.StatusInternalServerError)
		w.WriteHeader(http.StatusInternalServerError)
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
