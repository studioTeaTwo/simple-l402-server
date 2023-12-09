package main

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestIsAllow(t *testing.T) {
	reqBody := bytes.NewBufferString("request body")

	const wildcardOrigin = "https://hoge-studioteatwo.vercel.app"
	req := httptest.NewRequest(http.MethodGet, wildcardOrigin, reqBody)
	req.Header.Add("Origin", wildcardOrigin)
	result := isAllow(&req.Header)
	if !result {
		t.Errorf("isAllow() failed to match with origin: %s", wildcardOrigin)
	}

	const noListOrigin = "http://hoge:8000"
	req = httptest.NewRequest(http.MethodGet, noListOrigin, reqBody)
	req.Header.Add("Origin", noListOrigin)
	result = isAllow(&req.Header)
	if result == true {
		t.Errorf("isAllow() failed to match with origin: %s", noListOrigin)
	}
}
