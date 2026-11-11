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

func TestLoadgenOnlyDevelopmentAndLoadtestDB(t *testing.T) {
	for _, db := range []string{"racego_loadtest", "loadtest_racego"} {
		if err := LoadgenAllowed("development", db); err != nil {
			t.Fatalf("development + %s harus diizinkan: %v", db, err)
		}
	}
	for _, c := range [][2]string{
		{"production", "racego_loadtest"}, {"staging", "racego_loadtest"}, {"", "racego_loadtest"},
		{"development", "racego"}, {"development", "racego_test"}, {"development", "loadtest"}, {"development", "racego_loadtest_x"},
	} {
		if LoadgenAllowed(c[0], c[1]) == nil {
			t.Fatalf("APP_ENV=%q database=%q harus ditolak", c[0], c[1])
		}
	}
}
