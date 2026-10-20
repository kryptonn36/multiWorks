package cmd

import (
	"database/sql"
	"fmt"
	"log"
	"net/http"
	"os"
	"sync/atomic"

	"github.com/joho/godotenv"
	"github.com/kryptonn36/chirpy/internal/database"
	_ "github.com/lib/pq"
)

func main() {
	godotenv.Load()
	const filepathRoot = "."
	const port = "8080"

	// dbURL := os.Getenv("DB_URL")
	// if dbURL == "" {
	// 	log.Fatal("DB_URL must be set")
	// }
	// platform := os.Getenv("PLATFORM")
	// if platform == "" {
	// 	log.Fatal("PLATFORM must be set")
	// }
	// db, err := sql.Open("postgres", dbURL)
	// if err != nil{
	// 	fmt.Printf("Error in opening database connection %v",err)
	// }
	// dbQueries := database.New(db)
	// apicfg := apiConfig{
	// 	fileserverHits: atomic.Int32{},
	// 	queries:        dbQueries,
	// 	platform:       platform,
	// 	secret:         os.Getenv("SECRET"),
	// }

	srv := &http.Server{
		Addr:    ":" + port,
		Handler: mux,
	}

	log.Printf("Serving files from %s on port: %s\n", filepathRoot, port)
	log.Fatal(srv.ListenAndServe())
}

// func (cfg *apiConfig) handlerMetrics(w http.ResponseWriter, r *http.Request) {
// 	w.Header().Set("Content-Type", "text/html; charset=utf-8")
// 	w.WriteHeader(http.StatusOK)
// 	hits := cfg.fileserverHits.Load()
// 	w.Write([]byte(fmt.Sprintf("<html><body><h1>Welcome, Chirpy Admin</h1><p>Chirpy has been visited %d times!</p></body></html>", hits)))
// }

// func (cfg *apiConfig) middlewareMetricsInc(next http.Handler) http.Handler {
// 	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
// 		cfg.fileserverHits.Add(1)
// 		next.ServeHTTP(w, r)
// 	})
// }