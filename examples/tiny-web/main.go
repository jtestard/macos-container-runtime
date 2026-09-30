package main

import (
	"flag"
	"fmt"
	"log"
	"net/http"
)

func main() {
	port := flag.Int("port", 8080, "HTTP listening port")
	flag.Parse()
	if flag.NArg() != 0 || *port < 1 || *port > 65535 {
		log.Fatal("usage: server [--port 1..65535]")
	}
	mux := http.NewServeMux()
	mux.HandleFunc("/healthz", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/plain; charset=utf-8")
		_, _ = w.Write([]byte("ok\n"))
	})
	mux.HandleFunc("/", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/plain; charset=utf-8")
		_, _ = w.Write([]byte("hello from macOS arm64\n"))
	})
	log.Fatal(http.ListenAndServe(fmt.Sprintf(":%d", *port), http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		log.Printf("request method=%s uri=%q remote=%s", r.Method, r.URL.RequestURI(), r.RemoteAddr)
		mux.ServeHTTP(w, r)
	})))
}
