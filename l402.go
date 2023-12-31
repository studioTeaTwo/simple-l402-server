package main

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
	"regexp"

	"github.com/studioTeaTwo/aperture/lsat"
	"github.com/studioTeaTwo/aperture/mint"
	"github.com/studioTeaTwo/aperture/nostr"
)

// create the macaroon & invoice
type NewChallengeHandler struct {
	mint *mint.Mint
}

// verify the macaroon & preimage
type VerifyHandler struct {
	mint *mint.Mint
}

type Result struct {
	Result bool   `json:"result"`
	Reason string `json:"reason"`
}

func (nc *NewChallengeHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if result := isAllow(&r.Header); !result {
		w.WriteHeader(http.StatusForbidden)
		json.NewEncoder(w).Encode("")
		return
	}

	var p nostr.NostrPublishParam
	if err := json.NewDecoder(r.Body).Decode(&p); err != nil {
		log.Errorf("failed to parse parameters: %#v", err)
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	// TODO: validate more
	if p.UserNPubkey == "" || p.Slug == "" || p.Price == 0 {
		log.Errorf("invalid parameters: %#v", p)
		http.Error(w, "invalid parameters", http.StatusBadRequest)
		return
	}

	log.Infof("new challenge start: %#v", r.Header, p)

	res, macaroon, invoice := nc.mintAndFormat(&p)
	if !res.Result {
		log.Errorf("new challenge failed: %#v", res.Reason)
	} else {
		log.Infof("new challenge succeeded %v %v", macaroon, invoice)

		challenge := "L402 macaroon=" + macaroon + " invoice=" + invoice
		w.Header().Set("WWW-Authenticate", challenge)
	}

	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(http.StatusPaymentRequired)
	if err := json.NewEncoder(w).Encode(res); err != nil {
		log.Error(err)
	}
}

func (v *VerifyHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	log.Infof("verify start: %#v", r.Header)

	if result := isAllow(&r.Header); !result {
		w.WriteHeader(http.StatusForbidden)
		json.NewEncoder(w).Encode("")
		return
	}

	res := &Result{
		Result: true,
		Reason: "",
	}
	err := verify(&r.Header, v)
	if err != nil {
		res.Result = false
		res.Reason = err.Error()
	}
	log.Infof("verify end: %#v", res)

	status := http.StatusOK
	if err != nil {
		status = http.StatusPaymentRequired
	}

	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	if err := json.NewEncoder(w).Encode(res); err != nil {
		log.Error(err)
	}
}

func (nc NewChallengeHandler) mintAndFormat(params *nostr.NostrPublishParam) (res *Result, macaroon string, invoice string) {
	res = &Result{
		Result: true,
		Reason: "",
	}

	mac, invoice, err := nc.mint.MintLSAT(context.Background(), params, lsat.Service{
		Name:  SERVICE_NAME,
		Tier:  lsat.BaseTier,
		Price: params.Price,
	})
	if err != nil {
		log.Error(err)
		res.Result = false
		res.Reason = err.Error()
		return res, "", ""
	}

	log.Infof("macaroorn %#v", mac)
	macBytes, err := mac.MarshalBinary()
	if err != nil {
		log.Error(err)
		res.Result = false
		res.Reason = err.Error()
		return res, "", ""
	}

	macaroon = base64.StdEncoding.EncodeToString(macBytes)
	return res, macaroon, invoice
}

func verify(header *http.Header, v *VerifyHandler) error {
	mac, preimage, err := lsat.FromHeader(header)
	log.Infof("header %#v", header.Get("Authorization"))
	if err != nil {
		return fmt.Errorf("deny: %w", err)
	}
	verificationParams := &mint.VerificationParams{
		Macaroon:      mac,
		Preimage:      preimage,
		TargetService: SERVICE_NAME,
	}
	err = v.mint.VerifyLSAT(context.Background(), verificationParams)
	if err != nil {
		return fmt.Errorf("deny: %w", err)
	}
	return nil
}

func isAllow(h *http.Header) bool {
	orgin := h.Get("Origin")
	for _, v := range ALLOW_LIST {
		if v == "" {
			continue
		}
		r := regexp.MustCompile(v)
		if matched := r.MatchString(orgin); matched {
			return true
		}
		// preveiw branch
		r = regexp.MustCompile(`https://\S*-studioteatwo.vercel.app`)
		if matched := r.MatchString(orgin); matched {
			return true
		}
	}
	return false
}
