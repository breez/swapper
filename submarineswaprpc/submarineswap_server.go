package submarineswaprpc

import (
	"context"
	"encoding/hex"
	"fmt"
	"log"

	"swapper/mempoolspace"

	"github.com/breez/server/breez"
	"github.com/btcsuite/btcd/chaincfg"
	"github.com/lightningnetwork/lnd/lnrpc/submarineswaprpc"
	"github.com/lightningnetwork/lnd/submarineswap"
	"github.com/lightningnetwork/lnd/zpay32"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// Server is a sub-server of the main RPC server.
type Server struct {
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

// UnspentAmount returns the total amount of the btc received in a watched address
// and the height of the first transaction sending btc to the address.
func (s *Server) UnspentAmount(ctx context.Context,
	in *UnspentAmountRequest) (*UnspentAmountResponse, error) {

	address := in.Address
	var lockHeight int32
	addr, _, lh, err := submarineswap.AddressFromHash(s.ActiveNetParams, in.Hash)
	if err != nil {
		return nil, err
	}
	address = addr.String()
	lockHeight = int32(lh)

	utxos, err := submarineswap.GetUtxos(in.Hash)
	if err != nil {
		return nil, err
	}
	var totalAmount int64
	var u []*UnspentAmountResponse_Utxo
	for _, utxo := range utxos {
		u = append(u, &UnspentAmountResponse_Utxo{
			BlockHeight: utxo.BlockHeight,
			Amount:      int64(utxo.Value),
			Txid:        utxo.Hash.String(),
			Index:       utxo.Index,
		})
		totalAmount += int64(utxo.Value)
	}
	log.Infof("[UnspentAmount] address=%v, totalAmount=%v", address, totalAmount)
	return &UnspentAmountResponse{Amount: totalAmount, LockHeight: lockHeight, Utxos: u}, nil
}

// getSwapPayment
func (s *Server) GetSwapPayment(ctx context.Context,
	in *GetSwapPaymentRequest, max int64) (*GetSwapPaymentResponse, error) {
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
		return &GetSwapPaymentResponse{
			FundsExceededLimit: true,
			SwapError:          breez.GetSwapPaymentReply_FUNDS_EXCEED_LIMIT,
			PaymentError:       fmt.Sprintf("payment request amount: %v is greater than max allowed: %v", decodedAmt, maxAllowedDeposit),
		}, nil
	}
	log.Printf("GetSwapPayment - paying node %x amt = %v, maxAllowed = %v", decodedPayReq.Destination.SerializeCompressed(), decodedAmt, maxAllowedDeposit)

	utxos, err := submarineswap.UnspentAmount(ctx, &submarineswaprpc.UnspentAmountRequest{Hash: decodedPayReq.PaymentHash[:]})
	if err != nil {
		return nil, err
	}

	if len(utxos.Utxos) == 0 {
		return nil, status.Errorf(codes.Internal, "there are no UTXOs related to payment request")
	}

	fees, err := submarineswap.SubSwapServiceRedeemFees(s.ActiveNetParams, decodedPayReq.PaymentHash[:])
	if err != nil {
		log.Printf("GetSwapPayment - SubSwapServiceRedeemFees error: %v", err)
		return nil, status.Errorf(codes.Internal, "couldn't determine the redeem transaction fees")
	}
	log.Printf("GetSwapPayment - SubSwapServiceRedeemFees: %v for amount in utxos: %v amount in payment request: %v", fees, utxos.Amount, decodedAmt)
	if 2*utxos.Amount < 3*fees {
		log.Println("GetSwapPayment - utxo amount less than 1.5 fees. Cannot proceed")
		return &GetSwapPaymentResponse{
			FundsExceededLimit: true,
			SwapError:          breez.GetSwapPaymentReply_TX_TOO_SMALL,
			PaymentError:       "total UTXO not sufficient to create the redeem transaction",
		}, nil
	}

	// Determine if the amount in payment request is the same as in the address UTXOs
	if utxos.Amount != decodedAmt {
		return &GetSwapPaymentResponse{
			FundsExceededLimit: true,
			SwapError:          breez.GetSwapPaymentReply_INVOICE_AMOUNT_MISMATCH,
			PaymentError:       "total UTXO amount not equal to the amount in client's payment request",
		}, nil
	}

	// Get the current blockheight
	c := &mempoolspace.Client{baseUrl: "https://mempool.space/api"}
	BlockHeight, err := c.CurrentHeight()
	if err != nil {
		log.Printf("GetSwapPayment - GetInfo error: %v", err)
		return nil, status.Errorf(codes.Internal, "couldn't determine the current blockheight")
	}

	if 4*(int32(BlockHeight)-utxos.Utxos[0].BlockHeight) > 3*utxos.LockHeight {
		return &GetSwapPaymentResponse{
			FundsExceededLimit: true,
			SwapError:          breez.GetSwapPaymentReply_SWAP_EXPIRED,
			PaymentError:       "client transaction older than redeem block treshold",
		}, nil
	}
	//database entry
	err = insertSubswapPayment(hex.EncodeToString(decodedPayReq.PaymentHash[:]), in.PaymentRequest)
	if err != nil {
		log.Printf("GetSwapPayment - insertSubswapPayment paymentRequest: %v, error: %v", in.PaymentRequest, err)
		return nil, fmt.Errorf("error in insertSubswapPayment: %w", err)
	}

	// Redeem the transaction
	redeem, err := submarineswap.SubSwapServiceRedeem(s.ActiveNetParams, decodedPayReq.PaymentHash[:], in.Address)
	if err != nil {
		log.Printf("GetSwapPayment - couldn't redeem transaction for hash: %v, error: %v", hex.EncodeToString(decodedPayReq.PaymentHash[:]), err)
		return nil, err
	}
	err = updateSubswapPayment(hex.EncodeToString(decodedPayReq.PaymentHash[:]), redeem.Txid)
	if err != nil {
		log.Printf("GetSwapPayment - updateSubswapPayment preimage: %x, txid: %v, error: %v", sendResponse.PaymentPreimage, redeem.Txid, err)
		return nil, fmt.Errorf("error in updateSubswapPayment: %w", err)
	}

	log.Printf("GetSwapPayment - redeem tx broadcast: %v", redeem.Txid)
	return &GetSwapPaymentResponse{PaymentError: err}, nil

}
