package main

import (
	"context"
	"flag"
	"log"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"
	"time"

	"claude_code_proxy_dns/internal/admin"
	"claude_code_proxy_dns/internal/cert"
	"claude_code_proxy_dns/internal/config"
	"claude_code_proxy_dns/internal/frontend"
	"claude_code_proxy_dns/internal/proxy"
)

func main() {
	dataDir := flag.String("data", "./data", "data directory")
	flag.Parse()

	if err := os.MkdirAll(*dataDir, 0755); err != nil {
		log.Fatalf("Failed to create data directory: %v", err)
	}

	// Config
	configStore := config.NewStore(filepath.Join(*dataDir, "config.json"))

	// Certificates
	certMgr := cert.NewManager(*dataDir)
	caCert, caKey, err := certMgr.EnsureCA()
	if err != nil {
		log.Fatalf("Failed to initialize CA: %v", err)
	}
	if _, _, err := certMgr.EnsureServerCert(caCert, caKey); err != nil {
		log.Fatalf("Failed to initialize server certificate: %v", err)
	}

	certFile := certMgr.GetServerCertPath()
	keyFile := filepath.Join(*dataDir, "server.key")

	password := os.Getenv("ADMIN_PASSWORD")
	if password == "" {
		password = "admin123"
	}

	// Proxy server
	proxyServer := proxy.NewServer(configStore)
	go func() {
		if err := proxyServer.Start(":443", certFile, keyFile); err != nil {
			log.Fatalf("Proxy server error: %v", err)
		}
	}()

	// Admin server
	adminServer := admin.NewServer(&admin.AdminConfig{
		Password:   password,
		CertFile:   certFile,
		KeyFile:    keyFile,
		ConfigPath: filepath.Join(*dataDir, "config.json"),
	}, configStore, proxyServer)

	go func() {
		if err := adminServer.Start(":8442", frontend.DistFS); err != nil {
			log.Fatalf("Admin server error: %v", err)
		}
	}()

	log.Println("Claude Code Proxy DNS started")
	log.Printf("  Proxy:  https://localhost:443")
	log.Printf("  Admin:  https://localhost:8442")
	log.Printf("  CA cert: %s", certMgr.GetCACertPath())

	quit := make(chan os.Signal, 1)
	signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)
	<-quit

	log.Println("Shutting down...")
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	if err := proxyServer.Stop(ctx); err != nil {
		log.Printf("Proxy shutdown error: %v", err)
	}
	if err := adminServer.Stop(ctx); err != nil {
		log.Printf("Admin shutdown error: %v", err)
	}

	log.Println("Server stopped")
}
