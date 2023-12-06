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

	log.Infof("new challenge start: %#v", r.Header)

	res, macaroon, invoice := nc.mintAndFormat()
	if !res.Result {
		log.Errorf("new challenge failed: %#v", res.Reason)
	} else {
		log.Info("new challenge succeeded ", macaroon, invoice)

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
	if result := isAllow(&r.Header); !result {
		w.WriteHeader(http.StatusForbidden)
		json.NewEncoder(w).Encode("")
		return
	}

	log.Infof("verify start: %#v", r.Header)

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

func (nc NewChallengeHandler) mintAndFormat() (res *Result, macaroon string, invoice string) {
	res = &Result{
		Result: true,
		Reason: "",
	}

	mac, invoice, err := nc.mint.MintLSAT(context.Background(), lsat.Service{
		Name:  SERVICE_NAME,
		Tier:  lsat.BaseTier,
		Price: 1,
	})
	if err != nil {
		log.Error(err)
		res.Result = false
		res.Reason = err.Error()
		return res, "", ""
	}

	log.Infof("macaroorn ", mac)
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
		if v == "" {
			continue
		}
		r := regexp.MustCompile(v)
		if matched := r.MatchString(h.Get("Origin")); matched {
			return true
		}
	}
	return false
}
