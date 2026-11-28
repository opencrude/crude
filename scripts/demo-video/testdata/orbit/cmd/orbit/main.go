package main

import (
	"log"
	"net/http"
	"time"

	"orbit/internal/api"
)

func main() {
	srv := &http.Server{
		Addr:              ":8080",
		Handler:           api.NewRouter(api.NewStore()),
		ReadHeaderTimeout: 5 * time.Second,
	}
	log.Printf("orbit listening on %s", srv.Addr)
	log.Fatal(srv.ListenAndServe())
}
