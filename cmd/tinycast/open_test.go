package main

import "testing"

func TestLocalURL(t *testing.T) {
	cases := map[string]string{
		":18855":           "http://127.0.0.1:18855/",
		"127.0.0.1:18855":  "http://127.0.0.1:18855/",
		"0.0.0.0:9000":     "http://127.0.0.1:9000/",
		"192.168.1.5:8081": "http://192.168.1.5:8081/",
		"[::]:18855":       "http://127.0.0.1:18855/",
		"乱写的地址":            "http://127.0.0.1:18855/",
	}
	for in, want := range cases {
		if got := localURL(in); got != want {
			t.Errorf("localURL(%q) = %q, 期望 %q", in, got, want)
		}
	}
}
