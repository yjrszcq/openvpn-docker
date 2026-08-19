package main

import "testing"

func TestParseOrigins(t *testing.T) {
	origins, err := parseOrigins(" one.example,two.example:8443 ")
	if err != nil || len(origins) != 2 || origins[0] != "one.example" || origins[1] != "two.example:8443" {
		t.Fatalf("origins=%v err=%v", origins, err)
	}
	if origins, err := parseOrigins(""); err != nil || origins != nil {
		t.Fatalf("empty origins=%v err=%v", origins, err)
	}
	if _, err := parseOrigins("one.example,"); err == nil {
		t.Fatal("empty origin was accepted")
	}
	if _, err := parseOrigins("*,one.example"); err == nil {
		t.Fatal("wildcard mixed with an exact origin was accepted")
	}
}
