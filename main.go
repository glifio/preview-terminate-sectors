package main

import (
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"time"

	"github.com/rs/cors"
)

func getRoot(w http.ResponseWriter, r *http.Request) {
	fmt.Printf("got / request\n")
	for i := 0; i < 100; i++ {
		str := fmt.Sprintf("{\"seq\": %d}\n", i)
		io.WriteString(w, str)
		if f, ok := w.(http.Flusher); ok {
			f.Flush()
		}
		time.Sleep(1 * time.Second)
	}
}
func getHello(w http.ResponseWriter, r *http.Request) {
	fmt.Printf("got /hello request\n")
	io.WriteString(w, "Hello, HTTP!\n")
}

func main() {
	mux := http.NewServeMux()
	mux.HandleFunc("/", getRoot)
	mux.HandleFunc("/hello", getHello)
	handler := cors.Default().Handler(mux)
	err := http.ListenAndServe(":3000", handler)
	if errors.Is(err, http.ErrServerClosed) {
		fmt.Printf("server closed\n")
	} else if err != nil {
		fmt.Printf("error starting server: %s\n", err)
		os.Exit(1)
	}
}
