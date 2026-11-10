package main

import (
	"context"
	"errors"
	"log"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/nciyuan9264/game-platform/internal/plugins/othello"
)

func main() {
	if len(os.Args) == 2 && os.Args[1] == "self-test" {
		ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
		defer cancel()
		if err := othello.SelfTestEdax(ctx); err != nil {
			log.Fatal(err)
		}
		log.Print("Edax self-test passed")
		return
	}
	secret := strings.TrimSpace(os.Getenv("EDAX_SHARED_SECRET"))
	if len(secret) < 32 {
		log.Fatal("EDAX_SHARED_SECRET must contain at least 32 characters")
	}
	port := firstNonEmpty(
		os.Getenv("PORT"),
		os.Getenv("FC_CUSTOM_CONTAINER_PORT"),
		"9000",
	)
	server := &http.Server{
		Addr:              ":" + port,
		Handler:           othello.NewEdaxHTTPHandler(secret),
		ReadHeaderTimeout: 3 * time.Second,
		ReadTimeout:       5 * time.Second,
		WriteTimeout:      10 * time.Second,
		IdleTimeout:       30 * time.Second,
	}
	ctx, stop := signal.NotifyContext(
		context.Background(),
		syscall.SIGINT,
		syscall.SIGTERM,
	)
	defer stop()
	go func() {
		<-ctx.Done()
		shutdownCtx, cancel := context.WithTimeout(
			context.Background(),
			5*time.Second,
		)
		defer cancel()
		if err := server.Shutdown(shutdownCtx); err != nil {
			log.Printf("shutdown Edax service: %v", err)
		}
	}()
	log.Printf("Edax analysis service listening on :%s", port)
	if err := server.ListenAndServe(); !errors.Is(err, http.ErrServerClosed) {
		log.Fatal(err)
	}
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if value = strings.TrimSpace(value); value != "" {
			return value
		}
	}
	return ""
}
