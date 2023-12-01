package main

import (
	"context"

	"github.com/lightninglabs/aperture/lsat"
)

// mock of aperture/services.go

type mockServiceLimiter struct {
	capabilities map[lsat.Service]lsat.Caveat
	constraints  map[lsat.Service][]lsat.Caveat
	timeouts     map[lsat.Service]lsat.Caveat
}

func (l *mockServiceLimiter) ServiceCapabilities(ctx context.Context,
	services ...lsat.Service) ([]lsat.Caveat, error) {
	return make([]lsat.Caveat, 0, len(services)), nil
}
func (l *mockServiceLimiter) ServiceConstraints(ctx context.Context,
	services ...lsat.Service) ([]lsat.Caveat, error) {
	return make([]lsat.Caveat, 0, len(services)), nil
}
func (l *mockServiceLimiter) ServiceTimeouts(ctx context.Context,
	services ...lsat.Service) ([]lsat.Caveat, error) {
	return make([]lsat.Caveat, 0, len(services)), nil
}
