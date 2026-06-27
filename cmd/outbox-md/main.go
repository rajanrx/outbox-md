package main

import (
	"log"
	"net/http"
	"os"
)

func newMux() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("ok"))
	})
	return mux
}

func main() {
	addr := ":8080"
	if v := os.Getenv("OUTBOX_ADDR"); v != "" {
		addr = v
	}
	log.Printf("outbox-md listening on %s", addr)
	if err := http.ListenAndServe(addr, newMux()); err != nil {
		log.Fatal(err)
	}
}
