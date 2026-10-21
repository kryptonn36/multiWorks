package main

import (
	"context"
	"errors"
	"log"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/kryptonn36/multiWorks/internal/auth"
	"github.com/kryptonn36/multiWorks/internal/config"
	"github.com/kryptonn36/multiWorks/internal/db"
)

func main() {
	cfg, err := config.Load()
	if err != nil {
		log.Fatalf("load configuration: %v", err)
	}

	startupCtx, cancelStartup := context.WithTimeout(
		context.Background(),
		30*time.Second,
	)
	defer cancelStartup()

	database, err := db.Open(startupCtx, cfg.DatabaseURL)
	if err != nil {
		log.Fatalf("initialize database: %v", err)
	}
	defer database.Close()

	queries := db.New(database)
	authHandler := auth.NewHandler(
		queries,
		cfg.JWTSecret,
		15*time.Minute,
	)

	router := gin.New()
	router.Use(gin.Logger(), gin.Recovery())

	router.GET("/health", func(c *gin.Context) {
		ctx, cancel := context.WithTimeout(
			c.Request.Context(),
			2*time.Second,
		)
		defer cancel()

		if err := database.PingContext(ctx); err != nil {
			c.JSON(http.StatusServiceUnavailable, gin.H{
				"status": "unhealthy",
				"error":  "database unavailable",
			})
			return
		}

		c.JSON(http.StatusOK, gin.H{
			"status":   "ok",
			"service":  "cornflask-storyboard-api",
			"database": "connected",
		})
	})

	publicAuth := router.Group("/api/auth")
	publicAuth.POST("/register", authHandler.Register)
	publicAuth.POST("/login", authHandler.Login)

	protectedAuth := router.Group("/api/auth")
	protectedAuth.Use(auth.AuthMiddleware(cfg.JWTSecret))
	protectedAuth.GET("/me", authHandler.Me)

	server := &http.Server{
		Addr:              cfg.Port,
		Handler:           router,
		ReadHeaderTimeout: 5 * time.Second,
	}

	serverErrors := make(chan error, 1)
	go func() {
		log.Printf("API listening on %s", cfg.Port)
		serverErrors <- server.ListenAndServe()
	}()

	signalCtx, stop := signal.NotifyContext(
		context.Background(),
		os.Interrupt,
		syscall.SIGTERM,
	)
	defer stop()

	select {
	case err := <-serverErrors:
		if err != nil && !errors.Is(err, http.ErrServerClosed) {
			log.Fatalf("HTTP server: %v", err)
		}

	case <-signalCtx.Done():
		log.Println("Shutdown signal received")

		shutdownCtx, cancel := context.WithTimeout(
			context.Background(),
			10*time.Second,
		)
		defer cancel()

		if err := server.Shutdown(shutdownCtx); err != nil {
			log.Printf("graceful shutdown failed: %v", err)
			_ = server.Close()
		}
	}
}
