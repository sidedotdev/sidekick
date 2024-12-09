package main

import (
	"log"
	"sidekick/api"

	"github.com/joho/godotenv"
)

func main() {
	if err := godotenv.Load(); err != nil {
		log.Fatal(err)
	}

	if err := api.RunServer(); err != nil {
		log.Fatal(err)
	}
}
