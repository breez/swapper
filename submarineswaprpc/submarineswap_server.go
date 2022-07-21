package submarineswaprpc

import (
	"context"
	"log"

	"github.com/btcsuite/btcd/chaincfg"
	"github.com/lightningnetwork/lnd/submarineswap"
)

const (
	// subServerName is the name of the RPC sub-server. We'll use this name
	// to register ourselves, and we also require that the main
	// SubServerConfigDispatcher instance recognize this as the name of the
	// config file that we need.
	subServerName = "SubmarineSwapRPC"
)

// Server is a sub-server of the main RPC server.
type Server struct {
	started         uint32
	stopped         uint32
	ActiveNetParams *chaincfg.Params
}

// SubSwapServiceInit
func (s *Server) SubSwapServiceInit(ctx context.Context,
	in *SubSwapServiceInitRequest) (*SubSwapServiceInitResponse, error) {
	//Create a new submarine address and associated script
	addr, script, swapServicePubKey, lockHeight, err := submarineswap.NewSubmarineSwap(
		s.ActiveNetParams,
		in.Pubkey,
		in.Hash,
	)
	if err != nil {
		return nil, err
	}
	log.Infof("[SubSwapServiceInit] addr=%v script=%x pubkey=%x", addr.String(), script, swapServicePubKey)
	return &SubSwapServiceInitResponse{Address: addr.String(), Pubkey: swapServicePubKey, LockHeight: lockHeight}, nil
}
