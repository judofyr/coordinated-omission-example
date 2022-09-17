package main

import (
	"log"
	"net/http"
	"sync"
	"time"
)

var lock sync.RWMutex

func main() {
	go func() {
		for {
			time.Sleep(10*time.Second)
			lock.Lock()
			time.Sleep(2*time.Second)
			lock.Unlock()
		}
	}()

	http.HandleFunc("/", func (w http.ResponseWriter, req *http.Request)  {
		lock.RLock()
		defer lock.RUnlock()

		w.Write([]byte("ok"))
	})

	if err := http.ListenAndServe("127.0.0.1:8989", nil); err != nil {
		log.Fatalf("listen error: %s", err)
	}
}