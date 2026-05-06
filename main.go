package main

import (
	"log"
	"net/http"

	"mixfile/server"
)

func main() {
	ser1 := server.NewMixFileServer()

	log.Println("server start :8080")

	log.Fatal(http.ListenAndServe(":8080", ser1))
}
