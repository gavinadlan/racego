package main

import "testing"

func TestDevReleaseOnlyInDevelopment(t *testing.T) {
	if err := DevOnly("development"); err != nil {
		t.Fatal(err)
	}
	for _, env := range []string{"production", "staging", ""} {
		if DevOnly(env) == nil {
			t.Fatalf("APP_ENV=%q harus ditolak", env)
		}
	}
}
