package main

import (
	"log"
	"net/http"
)

func main() {
	mux := http.NewServeMux()
	mux.HandleFunc("/healthz", func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte("ok\n"))
	})
	mux.HandleFunc("/", func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte("hello from a native macOS Kubernetes Pod\n"))
	})
	log.Fatal(http.ListenAndServe(":8081", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		log.Printf("request method=%s uri=%q remote=%s", r.Method, r.URL.RequestURI(), r.RemoteAddr)
		mux.ServeHTTP(w, r)
	})))
}
