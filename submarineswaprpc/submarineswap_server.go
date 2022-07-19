//go:build submarineswaprpc
// +build submarineswaprpc

package submarineswaprpc

import (
	"context"
	"os"
	"path/filepath"
	"sync/atomic"

	"github.com/lightningnetwork/lnd/lnrpc"
	"github.com/lightningnetwork/lnd/lnwallet/btcwallet"
	"github.com/lightningnetwork/lnd/submarineswap"
)

const (
	// subServerName is the name of the RPC sub-server. We'll use this name
	// to register ourselves, and we also require that the main
	// SubServerConfigDispatcher instance recognize this as the name of the
	// config file that we need.
	subServerName = "SubmarineSwapRPC"
)

// fileExists reports whether the named file or directory exists.
func fileExists(name string) bool {
	if _, err := os.Stat(name); err != nil {
		if os.IsNotExist(err) {
			return false
		}
	}
	return true
}

type ServerShell struct {
	SubmarineSwapperServer
}

// Server is a sub-server of the main RPC server.
type Server struct {
	started uint32
	stopped uint32

	// Required by the grpc-gateway/v2 library for forward compatibility.
	// Must be after the atomically used variables to not break struct
	// alignment.
	UnimplementedSubmarineSwapperServer

	cfg Config
}

// New returns a new instance of the submarineswaprpc SubmarineSwapper
// sub-server. We also return the set of permissions for the macaroons
// that we may create within this method. If the macaroons we need aren't
// found in the filepath, then we'll create them on start up.
// If we're unable to locate, or create the macaroons we need, then we'll
// return with an error.
func New(cfg *Config) (*Server, error) {
	// If the path of the submarine swapper macaroon wasn't generated, then
	// we'll assume that it's found at the default network directory.
	if cfg.SubmarineSwapMacPath == "" {
		cfg.SubmarineSwapMacPath = filepath.Join(
			cfg.NetworkDir,
		)
	}

	return &Server{
		cfg: *cfg,
	}, nil
}

// Compile-time checks to ensure that Server fully implements the
// SubmarineSwapperServer gRPC service and lnrpc.SubServer interface.
var _ SubmarineSwapperServer = (*Server)(nil)
var _ lnrpc.SubServer = (*Server)(nil)

// Start launches any helper goroutines required for the server to function.
//
// NOTE: This is part of the lnrpc.SubServer interface.
func (s *Server) Start() error {
	if !atomic.CompareAndSwapUint32(&s.started, 0, 1) {
		return nil
	}

	return nil
}

// Stop signals any active goroutines for a graceful closure.
//
// NOTE: This is part of the lnrpc.SubServer interface.
func (s *Server) Stop() error {
	if !atomic.CompareAndSwapUint32(&s.stopped, 0, 1) {
		return nil
	}
	return nil
}

// Name returns a unique string representation of the sub-server. This can be
// used to identify the sub-server and also de-duplicate them.
//
// NOTE: This is part of the lnrpc.SubServer interface.
func (s *Server) Name() string {
	return subServerName
}

// SubSwapServiceInit
func (s *Server) SubSwapServiceInit(ctx context.Context,
	in *SubSwapServiceInitRequest) (*SubSwapServiceInitResponse, error) {
	b := s.cfg.Wallet.WalletController.(*btcwallet.BtcWallet).InternalWallet()
	//Create a new submarine address and associated script
	addr, script, swapServicePubKey, lockHeight, err := submarineswap.NewSubmarineSwap(
		b.Database(),
		b.Manager,
		s.cfg.ActiveNetParams,
		b.ChainClient(),
		s.cfg.Wallet.Cfg.Database,
		in.Pubkey,
		in.Hash,
	)
	if err != nil {
		return nil, err
	}
	log.Infof("[SubSwapServiceInit] addr=%v script=%x pubkey=%x", addr.String(), script, swapServicePubKey)
	return &SubSwapServiceInitResponse{Address: addr.String(), Pubkey: swapServicePubKey, LockHeight: lockHeight}, nil
}
