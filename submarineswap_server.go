package main

import (
	"context"
	"encoding/hex"
	"fmt"
	"log"
	"os"

	"swapper/mempoolspace"
	"swapper/submarineswaprpc"

	"github.com/btcsuite/btcd/chaincfg"
	"github.com/lightningnetwork/lnd/zpay32"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// Server is a sub-server of the main RPC server.
type Server struct {
	ActiveNetParams *chaincfg.Params
}

func getBlockchainClient() *mempoolspace.Client {
	client := &mempoolspace.Client{BaseUrl: os.Getenv("BASE_URL")}

	return client
}

// SubSwapServiceInit
func (s *Server) SubSwapServiceInit(ctx context.Context,
	in *submarineswaprpc.SubSwapServiceInitRequest) (*submarineswaprpc.SubSwapServiceInitResponse, error) {
	//Create a new submarine address and associated script
	addr, script, swapServicePubKey, lockHeight, err := newSubmarineSwap(
		s.ActiveNetParams,
		in.Pubkey,
		in.Hash,
	)
	if err != nil {
		return nil, err
	}
	log.Printf("[SubSwapServiceInit] addr=%v script=%x pubkey=%x", addr.String(), script, swapServicePubKey)
	return &submarineswaprpc.SubSwapServiceInitResponse{Address: addr.String(), Pubkey: swapServicePubKey, LockHeight: lockHeight}, nil
}

// unspentAmount returns the total amount of the btc received in a watched address
// and the height of the first transaction sending btc to the address.
func (s *Server) unspentAmount(ctx context.Context,
	in *submarineswaprpc.UnspentAmountRequest) (*submarineswaprpc.UnspentAmountResponse, error) {

	address := in.Address
	var lockHeight int32
	addr, _, lh, err := addressFromHash(s.ActiveNetParams, in.Hash)
	if err != nil {
		return nil, err
	}
	address = addr.String()
	lockHeight = int32(lh)
	c := getBlockchainClient()
	utxos, err := c.GetUtxos(in.Hash)
	if err != nil {
		return nil, err
	}
	var totalAmount int64
	var u []*submarineswaprpc.UnspentAmountResponse_Utxo
	for _, utxo := range utxos {
		u = append(u, &submarineswaprpc.UnspentAmountResponse_Utxo{
			BlockHeight: utxo.BlockHeight,
			Amount:      int64(utxo.Value),
			Txid:        utxo.Hash.String(),
			Index:       utxo.Index,
		})
		totalAmount += int64(utxo.Value)
	}
	log.Printf("[UnspentAmount] address=%v, totalAmount=%v", address, totalAmount)
	return &submarineswaprpc.UnspentAmountResponse{Amount: totalAmount, LockHeight: lockHeight, Utxos: u}, nil
}

// getSwapPayment
func (s *Server) GetSwapPayment(ctx context.Context,
	in *submarineswaprpc.GetSwapPaymentRequest, max int64) (*submarineswaprpc.GetSwapPaymentResponse, error) {
	// Decode the the client's payment request
	decodedPayReq, err := zpay32.Decode(in.PaymentRequest, s.ActiveNetParams)
	if err != nil {
		log.Printf("GetSwapPayment - Error in zpay32.Decode: %v", err)
		return nil, status.Errorf(codes.Internal, "payment request is not valid")
	}

	decodedAmt := int64(0)
	if decodedPayReq.MilliSat != nil {
		decodedAmt = int64(decodedPayReq.MilliSat.ToSatoshis())
	}

	maxAllowedDeposit := max
	if decodedAmt > maxAllowedDeposit {
		log.Printf("GetSwapPayment - decodedAmt > maxAllowedDeposit: %v > %v", decodedAmt, maxAllowedDeposit)
		return &submarineswaprpc.GetSwapPaymentResponse{
			FundsExceededLimit: true,
			SwapError:          submarineswaprpc.GetSwapPaymentResponse_FUNDS_EXCEED_LIMIT,
			PaymentError:       fmt.Sprintf("payment request amount: %v is greater than max allowed: %v", decodedAmt, maxAllowedDeposit),
		}, nil
	}
	log.Printf("GetSwapPayment - paying node %x amt = %v, maxAllowed = %v", decodedPayReq.Destination.SerializeCompressed(), decodedAmt, maxAllowedDeposit)

	utxos, err := s.unspentAmount(ctx, &submarineswaprpc.UnspentAmountRequest{Hash: decodedPayReq.PaymentHash[:]})
	if err != nil {
		return nil, err
	}

	if len(utxos.Utxos) == 0 {
		return nil, status.Errorf(codes.Internal, "there are no UTXOs related to payment request")
	}

	fees, err := subSwapServiceRedeemFees(s.ActiveNetParams, decodedPayReq.PaymentHash[:])
	if err != nil {
		log.Printf("GetSwapPayment - SubSwapServiceRedeemFees error: %v", err)
		return nil, status.Errorf(codes.Internal, "couldn't determine the redeem transaction fees")
	}
	log.Printf("GetSwapPayment - SubSwapServiceRedeemFees: %v for amount in utxos: %v amount in payment request: %v", fees, utxos.Amount, decodedAmt)
	if 2*utxos.Amount < 3*fees {
		log.Println("GetSwapPayment - utxo amount less than 1.5 fees. Cannot proceed")
		return &submarineswaprpc.GetSwapPaymentResponse{
			FundsExceededLimit: true,
			SwapError:          submarineswaprpc.GetSwapPaymentResponse_TX_TOO_SMALL,
			PaymentError:       "total UTXO not sufficient to create the redeem transaction",
		}, nil
	}

	// Determine if the amount in payment request is the same as in the address UTXOs
	if utxos.Amount != decodedAmt {
		return &submarineswaprpc.GetSwapPaymentResponse{
			FundsExceededLimit: true,
			SwapError:          submarineswaprpc.GetSwapPaymentResponse_INVOICE_AMOUNT_MISMATCH,
			PaymentError:       "total UTXO amount not equal to the amount in client's payment request",
		}, nil
	}

	// Get the current blockheight
	c := getBlockchainClient()
	BlockHeight, err := c.CurrentHeight()
	if err != nil {
		log.Printf("GetSwapPayment - GetInfo error: %v", err)
		return nil, status.Errorf(codes.Internal, "couldn't determine the current blockheight")
	}

	if 4*(int32(BlockHeight)-utxos.Utxos[0].BlockHeight) > 3*utxos.LockHeight {
		return &submarineswaprpc.GetSwapPaymentResponse{
			FundsExceededLimit: true,
			SwapError:          submarineswaprpc.GetSwapPaymentResponse_SWAP_EXPIRED,
			PaymentError:       "client transaction older than redeem block treshold",
		}, nil
	}
	//database entry
	err = insertSubswapPayment(hex.EncodeToString(decodedPayReq.PaymentHash[:]), in.PaymentRequest)
	if err != nil {
		log.Printf("GetSwapPayment - insertSubswapPayment paymentRequest: %v, error: %v", in.PaymentRequest, err)
		return nil, fmt.Errorf("error in insertSubswapPayment: %w", err)
	}
	address, _, _, _ := addressFromHash(s.ActiveNetParams, decodedPayReq.PaymentHash[:])
	// Redeem the transaction
	redeem, err := subSwapServiceRedeem(s.ActiveNetParams, decodedPayReq.PaymentHash[:], address)
	if err != nil {
		log.Printf("GetSwapPayment - couldn't redeem transaction for hash: %v, error: %v", hex.EncodeToString(decodedPayReq.PaymentHash[:]), err)
		return nil, err
	}
	err = updateSubswapPayment(hex.EncodeToString(decodedPayReq.PaymentHash[:]), redeem)
	if err != nil {
		log.Printf("GetSwapPayment - updateSubswapPayment , txid: %v, error: %v", redeem, err)
		return nil, fmt.Errorf("error in updateSubswapPayment: %w", err)
	}

	log.Printf("GetSwapPayment - redeem tx broadcast: %v", redeem)
	return &submarineswaprpc.GetSwapPaymentResponse{PaymentError: "error from c lightning node on send payment"}, nil //need to change PaymentError pass error from node on send payment request.

}
