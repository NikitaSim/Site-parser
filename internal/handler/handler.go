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
	parseService  service.ParseService
	searchService service.SearchService
}

func NewHandler(parseService service.ParseService, searchService service.SearchService) *Handler {
	return &Handler{parseService: parseService, searchService: searchService}
}

func (h *Handler) Parser(w http.ResponseWriter, r *http.Request) {
	results, err := h.parseService.Parse()
	if err != nil {
		http.Error(w, fmt.Sprint("Parser error: ", err), http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(results); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
}

type Messages struct {
	Message string `json:"message"`
}

func (h *Handler) Chat(w http.ResponseWriter, r *http.Request) {
	defer r.Body.Close()
	var message Messages
	decoder := json.NewDecoder(r.Body)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&message); err != nil {
		http.Error(w, fmt.Sprint("JSON read error: ", err), http.StatusBadRequest)
		return
	}

	response, err := h.searchService.Chat(message.Message)
	if errors.Is(err, service.ErrNeedClarification) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]string{
			"message": "Уточните запрос: укажите тип товара, бренд, коллекцию или артикул.",
		})
		return
	}
	if err != nil {
		http.Error(w, fmt.Sprint("Chat error: ", err), http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(response); err != nil {
		http.Error(w, fmt.Sprintf("JSON send error: %s", err), http.StatusInternalServerError)
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
		rw := &ResponseWriter{Status: http.StatusOK, ResponseWriter: w}
		start := time.Now()
		next.ServeHTTP(rw, r)
		fmt.Println("STATUS:", rw.Status, "Method:", r.Method, "Path:", r.URL.Path, "Time:", time.Since(start))
	})
}

func (h *Handler) RecoverMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer func() {
			if recover() != nil {
				http.Error(w, "server crash", http.StatusInternalServerError)
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
