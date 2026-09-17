package documents

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"log"
	"net/http"
	"strings"
	"time"
)

// NewHandler exposes document, asset, and storage configuration endpoints.
func NewHandler(service *Service) http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, _ *http.Request) {
		writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
	})
	mux.HandleFunc("GET /api/documents/tree", func(w http.ResponseWriter, _ *http.Request) {
		tree, err := service.Tree()
		if err != nil {
			writeError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, tree)
	})
	mux.HandleFunc("GET /api/documents/graph", func(w http.ResponseWriter, _ *http.Request) {
		graph, err := service.Graph()
		if err != nil {
			writeError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, graph)
	})
	mux.HandleFunc("GET /api/settings/storage", func(w http.ResponseWriter, _ *http.Request) {
		writeJSON(w, http.StatusOK, service.Storage())
	})
	mux.HandleFunc("GET /api/settings/application", func(w http.ResponseWriter, _ *http.Request) {
		writeJSON(w, http.StatusOK, service.ApplicationSettings())
	})
	mux.HandleFunc("PUT /api/settings/application", func(w http.ResponseWriter, r *http.Request) {
		var input ApplicationSettingsInput
		if !decodeJSON(w, r, &input) {
			return
		}
		settings, err := service.ConfigureApplication(input)
		if err != nil {
			writeError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, settings)
	})
	mux.HandleFunc("PUT /api/settings/storage", func(w http.ResponseWriter, r *http.Request) {
		var input StorageInput
		if !decodeJSON(w, r, &input) {
			return
		}
		settings, err := service.ConfigureStorage(input.Path)
		if err != nil {
			writeError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, settings)
	})
	mux.HandleFunc("GET /api/settings/directories", func(w http.ResponseWriter, r *http.Request) {
		listing, err := service.BrowseDirectories(r.URL.Query().Get("path"))
		if err != nil {
			writeError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, listing)
	})
	mux.HandleFunc("GET /api/documents/content", func(w http.ResponseWriter, r *http.Request) {
		document, err := service.Get(r.URL.Query().Get("path"))
		if err != nil {
			writeError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, document)
	})
	mux.HandleFunc("POST /api/documents", func(w http.ResponseWriter, r *http.Request) {
		var input CreateInput
		if !decodeJSON(w, r, &input) {
			return
		}
		if err := service.Create(input); err != nil {
			writeError(w, err)
			return
		}
		writeJSON(w, http.StatusCreated, map[string]string{"path": input.Path})
	})
	mux.HandleFunc("PUT /api/documents", func(w http.ResponseWriter, r *http.Request) {
		var input UpdateInput
		if !decodeJSON(w, r, &input) {
			return
		}
		if err := service.Update(input); err != nil {
			writeError(w, err)
			return
		}
		path := input.Path
		if input.NewPath != nil {
			path = *input.NewPath
		}
		writeJSON(w, http.StatusOK, map[string]string{"path": path})
	})
	mux.HandleFunc("DELETE /api/documents", func(w http.ResponseWriter, r *http.Request) {
		if err := service.Delete(r.URL.Query().Get("path")); err != nil {
			writeError(w, err)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	})
	mux.HandleFunc("POST /api/assets", func(w http.ResponseWriter, r *http.Request) {
		r.Body = http.MaxBytesReader(w, r.Body, 11<<20)
		if err := r.ParseMultipartForm(10 << 20); err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "image is missing or exceeds 10 MB"})
			return
		}
		if r.MultipartForm != nil {
			defer r.MultipartForm.RemoveAll()
		}
		file, _, err := r.FormFile("file")
		if err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "image field 'file' is required"})
			return
		}
		defer file.Close()
		data, err := io.ReadAll(io.LimitReader(file, (10<<20)+1))
		if err != nil || len(data) > 10<<20 {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "image exceeds 10 MB"})
			return
		}
		asset, err := service.StoreAsset(data)
		if err != nil {
			writeError(w, err)
			return
		}
		writeJSON(w, http.StatusCreated, map[string]string{"url": "/api/assets/" + asset.Name})
	})
	mux.HandleFunc("GET /api/assets/{name}", func(w http.ResponseWriter, r *http.Request) {
		asset, err := service.Asset(r.PathValue("name"))
		if err != nil {
			writeError(w, err)
			return
		}
		w.Header().Set("Content-Type", asset.ContentType)
		w.Header().Set("Cache-Control", "public, max-age=31536000, immutable")
		http.ServeContent(w, r, asset.Name, time.Time{}, bytes.NewReader(asset.Data))
	})
	return cors(mux)
}

// decodeJSON reads one bounded JSON object and rejects unknown fields or trailing values.
func decodeJSON(w http.ResponseWriter, r *http.Request, target any) bool {
	if !strings.HasPrefix(r.Header.Get("Content-Type"), "application/json") {
		writeJSON(w, http.StatusUnsupportedMediaType, map[string]string{"error": "Content-Type must be application/json"})
		return false
	}
	decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<20))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(target); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid JSON body"})
		return false
	}
	if err := decoder.Decode(&struct{}{}); err != io.EOF {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "request body must contain one JSON value"})
		return false
	}
	return true
}

// writeJSON sends a JSON response with a consistent content type.
func writeJSON(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	if err := json.NewEncoder(w).Encode(value); err != nil {
		log.Printf("write JSON response: %v", err)
	}
}

// writeError maps domain errors to stable HTTP status codes.
func writeError(w http.ResponseWriter, err error) {
	status := http.StatusInternalServerError
	switch {
	case errors.Is(err, ErrForbidden):
		status = http.StatusForbidden
	case errors.Is(err, ErrNotFound):
		status = http.StatusNotFound
	case errors.Is(err, ErrAlreadyExists):
		status = http.StatusConflict
	case errors.Is(err, ErrInvalidPath), errors.Is(err, ErrInvalidType), errors.Is(err, ErrInvalidPageType), errors.Is(err, ErrInvalidSettings), errors.Is(err, ErrInvalidAsset):
		status = http.StatusBadRequest
	}
	writeJSON(w, status, map[string]string{"error": err.Error()})
}

// cors permits the local Vite development server without opening arbitrary origins.
func cors(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		origin := r.Header.Get("Origin")
		if origin == "http://localhost:5173" || origin == "http://127.0.0.1:5173" {
			w.Header().Set("Access-Control-Allow-Origin", origin)
			w.Header().Set("Vary", "Origin")
			w.Header().Set("Access-Control-Allow-Methods", "GET, POST, PUT, DELETE, OPTIONS")
			w.Header().Set("Access-Control-Allow-Headers", "Content-Type")
		}
		if r.Method == http.MethodOptions {
			w.WriteHeader(http.StatusNoContent)
			return
		}
		next.ServeHTTP(w, r)
	})
}
