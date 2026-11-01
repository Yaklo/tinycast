package main

import "testing"

func TestLocalURL(t *testing.T) {
	cases := map[string]string{
		":8080":            "http://127.0.0.1:8080/",
		"127.0.0.1:8080":   "http://127.0.0.1:8080/",
		"0.0.0.0:9000":     "http://127.0.0.1:9000/",
		"192.168.1.5:8081": "http://192.168.1.5:8081/",
		"[::]:8080":        "http://127.0.0.1:8080/",
		"乱写的地址":            "http://127.0.0.1:8080/",
	}
	for in, want := range cases {
		if got := localURL(in); got != want {
			t.Errorf("localURL(%q) = %q, 期望 %q", in, got, want)
		}
	}
}
