// Package nick generates and validates the anonymous nicknames people chat
// under, like "quiet-otter".
//
// It is the smallest, most self-contained package in termchat: no network,
// no terminal, no state. Everything else can depend on it; it depends on
// nothing but the standard library.
package nick

import (
	"math/rand/v2"
	"unicode"
	"unicode/utf8"
)

// MaxLen is the maximum nick length in runes (Unicode code points), not bytes.
const MaxLen = 24

// Lowercase names are unexported (private to this package).
var adjectives = []string{
	"quiet", "brave", "sleepy", "clever", "fuzzy", "sunny", "witty", "gentle",
	"swift", "curious", "lucky", "mellow", "bold", "calm", "jolly", "shy",
	"zany", "cosmic", "dusty", "misty", "nimble", "plucky", "rusty", "silent",
}

var animals = []string{
	"otter", "panda", "falcon", "gecko", "koala", "lynx", "moose", "newt",
	"owl", "penguin", "quokka", "raven", "seal", "tapir", "walrus", "yak",
	"badger", "bison", "crane", "dingo", "ferret", "heron", "ibis", "lemur",
}

// Random returns a nick of the form "adjective-animal".
//
// It takes the random source as a parameter instead of using a global one,
// so tests can pass a fixed seed and get repeatable results. (In Python terms:
// passing a random.Random(seed) instead of calling random.choice directly.)
func Random(r *rand.Rand) string {
	rAdjective := adjectives[r.IntN(len(adjectives))]
	rAnimal := animals[r.IntN(len(animals))]
	return rAdjective + "-" + rAnimal
}

// Valid reports whether s is an acceptable nick. The rules are:
//   - between 1 and MaxLen runes long
//   - every rune is a letter, a digit, '-' or '_'
//
// "Letter" means any Unicode letter, so "café" is valid but an emoji is not.
func Valid(s string) bool {
	if n := utf8.RuneCountInString(s); n < 1 || n > MaxLen {
		return false
	}
	for _, r := range s {
		if !unicode.IsLetter(r) && !unicode.IsDigit(r) && r != '-' && r != '_' {
			return false
		}
	}
	return true
}
