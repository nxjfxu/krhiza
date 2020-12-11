package greeting

import (
	"crypto/rand"
	"log"
)

var words []string = []string{
	"Hello",
	"Hi",
	"Ahoy",
	"Good morning",
	"Good afternoon",
	"Good Evening",
	"My friend",
	"How are you",
	"Nice to meet you",
	"Alice",
	"Bob",
	"Eve",
}

func Greeting(wc int) string {
	rs := make([]byte, wc)
	_, err := rand.Read(rs[:])
	if err != nil {
		log.Fatalf("Random failed.  Why?")
	}

	sentence := ""
	for i := 0; i < wc; i++ {
		sentence += words[int(rs[i])%len(words)]
		if i < wc-1 {
			sentence += " "
		}
	}
	sentence += "."
	return sentence
}
