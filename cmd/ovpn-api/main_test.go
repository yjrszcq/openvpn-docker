package main

import "testing"

func TestParseOrigins(t *testing.T) {
	origins, err := parseOrigins(" https://one.example,https://two.example ")
	if err != nil || len(origins) != 2 || origins[0] != "https://one.example" || origins[1] != "https://two.example" {
		t.Fatalf("origins=%v err=%v", origins, err)
	}
	if origins, err := parseOrigins(""); err != nil || origins != nil {
		t.Fatalf("empty origins=%v err=%v", origins, err)
	}
	if _, err := parseOrigins("https://one.example,"); err == nil {
		t.Fatal("empty origin was accepted")
	}
}
