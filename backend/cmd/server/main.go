
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
	"github.com/kryptonn36/multiWorks/internal/project"
	"github.com/kryptonn36/multiWorks/internal/workspace"
	"github.com/kryptonn36/multiWorks/internal/storyboard"
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
	workspaceHandler := workspace.NewHandler(database)
	projectHandler := project.NewHandler(database)
	storyboardHandler := storyboard.NewHandler(database)

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

	// Public authentication endpoints.
	publicAuth := router.Group("/api/auth")
	publicAuth.POST("/register", authHandler.Register)
	publicAuth.POST("/login", authHandler.Login)

	// Authenticated user profile.
	protectedAuth := router.Group("/api/auth")
	protectedAuth.Use(auth.AuthMiddleware(cfg.JWTSecret))
	protectedAuth.GET("/me", authHandler.Me)

	// All workspace and project routes require a valid JWT.
	api := router.Group("/api")
	api.Use(auth.AuthMiddleware(cfg.JWTSecret))

	api.POST("/workspaces", workspaceHandler.Create)
	api.GET("/workspaces", workspaceHandler.List)
	api.GET("/workspaces/:workspaceID", workspaceHandler.Get)

	api.GET(
		"/workspaces/:workspaceID/members",
		workspaceHandler.ListMembers,
	)
	api.POST(
		"/workspaces/:workspaceID/members",
		workspaceHandler.AddMember,
	)

	api.POST(
		"/workspaces/:workspaceID/projects",
		projectHandler.Create,
	)
	api.GET(
		"/workspaces/:workspaceID/projects",
		projectHandler.List,
	)

	api.GET("/projects/:projectID", projectHandler.Get)
	api.GET("/projects/:projectID/members", projectHandler.ListMembers)
	api.POST("/projects/:projectID/members", projectHandler.AddMember)

	// Storyboard
	api.GET("/projects/:projectID/board", storyboardHandler.GetBoard)

	// Scenes
	api.POST("/projects/:projectID/scenes", storyboardHandler.CreateScene)
	api.PATCH("/projects/:projectID/scenes/:sceneID", storyboardHandler.UpdateScene)
	api.DELETE("/projects/:projectID/scenes/:sceneID", storyboardHandler.DeleteScene)
	api.PUT("/projects/:projectID/scenes/order", storyboardHandler.ReorderScenes)

	// Shots
	api.POST(
		"/projects/:projectID/scenes/:sceneID/shots",
		storyboardHandler.CreateShot,
	)
	api.PATCH(
		"/projects/:projectID/scenes/:sceneID/shots/:shotID",
		storyboardHandler.UpdateShot,
	)
	api.DELETE(
		"/projects/:projectID/scenes/:sceneID/shots/:shotID",
		storyboardHandler.DeleteShot,
	)
	api.PUT(
		"/projects/:projectID/scenes/:sceneID/shots/order",
		storyboardHandler.ReorderShots,
)

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
