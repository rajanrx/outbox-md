package main

import "testing"

func TestPortOf(t *testing.T) {
	cases := map[string]string{
		":8181":           "8181",
		"localhost:8181":  "8181",
		"0.0.0.0:9090":    "9090",
		"127.0.0.1:3000":  "3000",
		"8181":            "8181", // a bare port number
		"":                "",
		"localhost":       "", // host with no port
	}
	for addr, want := range cases {
		if got := portOf(addr); got != want {
			t.Errorf("portOf(%q) = %q, want %q", addr, got, want)
		}
	}
}
