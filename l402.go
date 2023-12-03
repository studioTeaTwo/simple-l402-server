package main

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"log"
	"net/http"

	"github.com/lightninglabs/aperture/lsat"
	"github.com/lightninglabs/aperture/mint"
)

// create the macaroon & invoice
type NewChallengeHandler struct {
	mint *mint.Mint
}

// verify the macaroon & preimage
type VerifyHandler struct {
	mint *mint.Mint
}

type NewChallengeResult struct {
	Macaroon string `json:"macaroon"`
	Invoice  string `json:"invoice"`
}

type VerifyResult struct {
	Result bool   `json:"result"`
	Reason string `json:"reason"`
}

func (nc NewChallengeHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if result := isAllow(&r.Header); !result {
		w.WriteHeader(http.StatusForbidden)
		json.NewEncoder(w).Encode("")
		return
	}

	log.Println("new challenge start ", r.Header)

	mac, invoice, err := nc.mint.MintLSAT(context.Background(), lsat.Service{
		Name:  SERVICE_NAME,
		Tier:  lsat.BaseTier,
		Price: 1,
	})
	if err != nil {
		log.Println(err)
		return
	}

	log.Println("macaroorn ", mac)
	macBytes, err := mac.MarshalBinary()
	if err != nil {
		log.Println(err)
		return
	}
	macaroon := base64.StdEncoding.EncodeToString(macBytes)

	log.Println("new challenge succeeded ", macaroon, invoice)
	res := &NewChallengeResult{
		Macaroon: macaroon,
		Invoice:  invoice,
	}

	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(http.StatusPaymentRequired)
	if err := json.NewEncoder(w).Encode(res); err != nil {
		log.Println(err)
	}
}

func (v VerifyHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if result := isAllow(&r.Header); !result {
		w.WriteHeader(http.StatusForbidden)
		json.NewEncoder(w).Encode("")
		return
	}

	log.Println("verify start ", r.Header)

	res := &VerifyResult{
		Result: true,
		Reason: "",
	}
	err := verify(&r.Header, &v)
	if err != nil {
		res.Result = false
		res.Reason = err.Error()
	}
	log.Println("verify end ", res)

	status := http.StatusOK
	if err != nil {
		status = http.StatusPaymentRequired
	}

	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	if err := json.NewEncoder(w).Encode(res); err != nil {
		log.Println(err)
	}
}

func verify(header *http.Header, v *VerifyHandler) error {
	mac, preimage, err := lsat.FromHeader(header)
	log.Println("header", header.Get("Authorization"))
	if err != nil {
		return fmt.Errorf("deny: %v", err)
	}
	verificationParams := &mint.VerificationParams{
		Macaroon:      mac,
		Preimage:      preimage,
		TargetService: SERVICE_NAME,
	}
	err = v.mint.VerifyLSAT(context.Background(), verificationParams)
	if err != nil {
		return fmt.Errorf("deny: %v", err)
	}
	return nil
}

func isAllow(h *http.Header) bool {
	for _, v := range ALLOW_LIST {
		if v == h.Get("Origin") {
			return true
		}
	}
	return false
}
