// Command keygen membuat pasangan kunci Ed25519 untuk menandatangani QR tiket.
package main

import (
	"fmt"
	"log"

	"github.com/gavinadlan/racego/backend/internal/ticket"
)

func main() {
	seed, pub, err := ticket.GenerateSeed()
	if err != nil {
		log.Fatal(err)
	}
	fmt.Println("Tempel ke .env (RAHASIA, jangan di-commit):")
	fmt.Println("QR_PRIVATE_KEY=" + seed)
	fmt.Println()
	fmt.Println("Kunci publik (boleh dibagikan, dipakai scanner offline):")
	fmt.Println(pub)
}
