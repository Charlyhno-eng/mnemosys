package main

import (
	"flag"
	"log"
	"net/http"
	"path/filepath"

	"github.com/charly/mnemosys/backend/documents"
)

// main configures and starts the Mnemosys HTTP API.
func main() {
	address := flag.String("addr", "127.0.0.1:8080", "HTTP listen address")
	documentsPath := flag.String("documents-path", "data/documents", "directory containing Markdown documents")
	configPath := flag.String("config-path", "config/config.toml", "application configuration file")
	flag.Parse()

	root, err := filepath.Abs(*documentsPath)
	if err != nil {
		log.Fatalf("resolve documents path: %v", err)
	}
	config, err := filepath.Abs(*configPath)
	if err != nil {
		log.Fatalf("resolve settings path: %v", err)
	}

	service, err := documents.NewConfigurableService(root, config)
	if err != nil {
		log.Fatalf("initialize documents service: %v", err)
	}

	server := &http.Server{
		Addr:    *address,
		Handler: documents.NewHandler(service),
	}
	log.Printf("Mnemosys API listening on http://%s (documents: %s)", *address, service.Storage().Path)
	if err := server.ListenAndServe(); err != nil && err != http.ErrServerClosed {
		log.Fatal(err)
	}
}
