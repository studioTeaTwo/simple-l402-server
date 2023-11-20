package main

import (
	"log"
	"net/http"

	"github.com/rs/cors"
)

func main() {
	mux := http.NewServeMux()
	mux.Handle("/count", new(countHandler))

	c := cors.New(cors.Options{
		AllowedOrigins:   []string{"http://localhost:5137", "http://localhost:8080", "https://studio.teatwo.dev/self-sovereign-blog"},
		AllowedMethods:   []string{http.MethodGet, http.MethodPost, http.MethodDelete, http.MethodOptions},
		AllowedHeaders:   []string{"*"},
		AllowCredentials: true,
	})
	handler := c.Handler(mux)

	log.Fatal(http.ListenAndServe(":8080", handler))
}
