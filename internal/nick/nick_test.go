package nick

// This test file is the spec for M0. Don't edit it; make it pass.
//
// Because it declares `package nick` (not `package nick_test`), it lives
// *inside* the package and can see unexported names like `adjectives`.

import (
	"math/rand/v2"
	"slices"
	"strings"
	"testing"
)

func TestRandomFormat(t *testing.T) {
	r := rand.New(rand.NewPCG(1, 2)) // fixed seed → same sequence every run

	for i := 0; i < 100; i++ {
		got := Random(r)

		adj, animal, ok := strings.Cut(got, "-")
		if !ok {
			t.Fatalf("Random() = %q, want \"adjective-animal\"", got)
		}
		if !slices.Contains(adjectives, adj) {
			t.Errorf("Random() = %q: %q is not in adjectives", got, adj)
		}
		if !slices.Contains(animals, animal) {
			t.Errorf("Random() = %q: %q is not in animals", got, animal)
		}
		if !Valid(got) {
			t.Errorf("Random() = %q, but Valid(%q) = false", got, got)
		}
	}
}

func TestRandomIsDeterministicForSameSeed(t *testing.T) {
	a := rand.New(rand.NewPCG(42, 42))
	b := rand.New(rand.NewPCG(42, 42))

	for i := 0; i < 10; i++ {
		if x, y := Random(a), Random(b); x != y {
			t.Fatalf("call %d: same seed gave %q and %q", i, x, y)
		}
	}
}

func TestRandomHasVariety(t *testing.T) {
	r := rand.New(rand.NewPCG(7, 7))
	seen := map[string]bool{}

	for i := 0; i < 50; i++ {
		seen[Random(r)] = true
	}
	if len(seen) < 20 {
		t.Errorf("50 calls produced only %d distinct nicks; want at least 20", len(seen))
	}
}

// A "table-driven test": one slice of cases, one loop. This is the most
// common test style in Go — you'll write every test this way from M1 on.
func TestValid(t *testing.T) {
	tests := []struct {
		name string
		in   string
		want bool
	}{
		{"typical", "quiet-otter", true},
		{"underscore and digits", "neo_42", true},
		{"single char", "a", true},
		{"unicode letters", "café-señor", true},
		{"exactly 24 ascii", strings.Repeat("a", 24), true},
		{"exactly 24 multibyte runes", strings.Repeat("é", 24), true}, // 48 bytes, 24 runes
		{"empty", "", false},
		{"25 chars", strings.Repeat("a", 25), false},
		{"space", "quiet otter", false},
		{"slash", "a/b", false},
		{"ansi escape", "\x1b[2Jhacked", false},
		{"newline", "line1\nline2", false},
		{"emoji", "otter🦦", false},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := Valid(tc.in); got != tc.want {
				t.Errorf("Valid(%q) = %v, want %v", tc.in, got, tc.want)
			}
		})
	}
}
