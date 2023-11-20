package main

import (
	"fmt"
	"net/http"
	"sync"
)

type countHandler struct {
	mutex sync.Mutex // guards count
	count int
}

func (h *countHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	h.mutex.Lock()
	defer h.mutex.Unlock()
	h.count++
	fmt.Fprintf(w, "Count: %d\n", h.count)
}
