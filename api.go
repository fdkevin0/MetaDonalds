package main

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log"
	"mime"
	"net/http"
	"strings"
)

type server struct {
	search func(context.Context, string) ([]torrent, error)
	submit func(context.Context, string) error
}

func (s server) routes() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte("ok\n"))
	})
	mux.HandleFunc("GET /api/v1/search", func(w http.ResponseWriter, r *http.Request) {
		query := strings.TrimSpace(r.URL.Query().Get("q"))
		if query == "" || len(query) > 200 {
			http.Error(w, "q must be 1–200 bytes", http.StatusBadRequest)
			return
		}
		results, err := s.search(r.Context(), query)
		if err != nil {
			log.Printf("search failed: %v", err)
			http.Error(w, "search failed", http.StatusBadGateway)
			return
		}
		writeJSON(w, map[string]any{"results": results})
	})
	mux.HandleFunc("POST /api/v1/submit", func(w http.ResponseWriter, r *http.Request) {
		mediaType, _, _ := mime.ParseMediaType(r.Header.Get("Content-Type"))
		if mediaType != "application/json" {
			http.Error(w, "Content-Type must be application/json", http.StatusUnsupportedMediaType)
			return
		}
		r.Body = http.MaxBytesReader(w, r.Body, 1024)
		decoder := json.NewDecoder(r.Body)
		decoder.DisallowUnknownFields()
		var input struct {
			InfoHash string `json:"info_hash"`
		}
		if err := decoder.Decode(&input); err != nil {
			http.Error(w, "invalid JSON body", http.StatusBadRequest)
			return
		}
		if err := decoder.Decode(new(any)); !errors.Is(err, io.EOF) {
			http.Error(w, "expected one JSON object", http.StatusBadRequest)
			return
		}
		if !infoHashPattern.MatchString(input.InfoHash) {
			http.Error(w, "info_hash must be 40 hexadecimal characters", http.StatusBadRequest)
			return
		}
		hash := strings.ToLower(input.InfoHash)
		if err := s.submit(r.Context(), hash); err != nil {
			log.Printf("submit failed for %s: %v", hash, err)
			http.Error(w, "submission failed", http.StatusBadGateway)
			return
		}
		writeJSON(w, map[string]string{"status": "submitted", "info_hash": hash})
	})
	return mux
}

func writeJSON(w http.ResponseWriter, value any) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(value)
}
