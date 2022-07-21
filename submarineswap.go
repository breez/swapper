package main

import (
	"context"
	"crypto/sha256"
	"crypto/x509"
	"errors"
	"log"
	"net"
	"os"
	"strings"

	"github.com/btcsuite/btcd/btcec"
	"github.com/btcsuite/btcd/chaincfg"
	"github.com/btcsuite/btcd/txscript"
	"github.com/btcsuite/btcutil"
	grpc_middleware "github.com/grpc-ecosystem/go-grpc-middleware"
	"github.com/lightningnetwork/lnd/input"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"
)

const (
	defaultLockHeight = 288
)

var (
	submarineBucket      = []byte("submarineTransactions")
	wtxmgrNamespaceKey   = []byte("wtxmgr")
	waddrmgrNamespaceKey = []byte("waddrmgr")
)

func generateSubmarineSwapScript(swapperPubKey, payerPubKey, hash []byte, lockHeight int64) ([]byte, error) {
	builder := txscript.NewScriptBuilder()

	builder.AddOp(txscript.OP_HASH160)
	builder.AddData(input.Ripemd160H(hash))
	builder.AddOp(txscript.OP_EQUAL) // Leaves 0P1 (true) on the stack if preimage matches
	builder.AddOp(txscript.OP_IF)
	builder.AddData(swapperPubKey) // Path taken if preimage matches
	builder.AddOp(txscript.OP_ELSE)
	builder.AddInt64(lockHeight)
	builder.AddOp(txscript.OP_CHECKSEQUENCEVERIFY)
	builder.AddOp(txscript.OP_DROP)
	builder.AddData(payerPubKey) // Refund back to payer
	builder.AddOp(txscript.OP_ENDIF)
	builder.AddOp(txscript.OP_CHECKSIG)

	return builder.Script()
}

func NewSubmarineSwap(net *chaincfg.Params, pubKey, hash []byte) (address btcutil.Address, script, swapperPubKey []byte, lockHeight int64, err error) {

	if len(pubKey) != btcec.PubKeyBytesLenCompressed {
		err = errors.New("pubKey not valid")
		return
	}

	if len(hash) != 32 {
		err = errors.New("hash not valid")
		return
	}
	//Need to check that the hash doesn't already exists in our db
	_, _, _, _, errGet := getSwapperSubmarineData(hash)
	if errGet == nil {
		err = errors.New("Hash already exists")
		return
	}
	//Create swapperKey and swapperPubKey
	key, err := btcec.NewPrivateKey(btcec.S256())
	if err != nil {
		return
	}
	swapperKey := key.Serialize()
	swapperPubKey = key.PubKey().SerializeCompressed()
	lockHeight = defaultLockHeight

	//Create the script
	script, err = generateSubmarineSwapScript(swapperPubKey, pubKey, hash, defaultLockHeight)
	if err != nil {
		return
	}

	address, err = newAddressWitnessScriptHash(script, net)
	if err != nil {
		return
	}

	//Need to save the data into postgres
	err = saveSwapperSubmarineData(net.ScriptHashAddrID, hash, lockHeight, swapperKey, script)

	return
}
func newAddressWitnessScriptHash(script []byte, net *chaincfg.Params) (*btcutil.AddressWitnessScriptHash, error) {
	witnessProg := sha256.Sum256(script)
	return btcutil.NewAddressWitnessScriptHash(witnessProg[:], net)
}

func main() {

	err := pgConnect()
	if err != nil {
		log.Fatalf("pgConnect() error: %v", err)
	}

	address := os.Getenv("LISTEN_ADDRESS")
	var lis net.Listener

	lis, err = net.Listen("tcp", address)
	if err != nil {
		log.Fatalf("failed to listen: %v", err)
	}

	// Creds file to connect to gRPC
	cp := x509.NewCertPool()
	if !cp.AppendCertsFromPEM([]byte(strings.Replace(os.Getenv("CERT"), "\\n", "\n", -1))) {
		log.Fatalf("credentials: failed to append certificates")
	}
	creds := credentials.NewClientTLSFromCert(cp, "")

	conn, err := grpc.Dial(os.Getenv("ADDRESS"), grpc.WithTransportCredentials(creds))
	if err != nil {
		log.Fatalf("Failed to connect to gRPC: %v", err)
	}
	defer conn.Close()

	s := grpc.NewServer(
		grpc_middleware.WithUnaryServerChain(func(ctx context.Context, req interface{}, info *grpc.UnaryServerInfo, handler grpc.UnaryHandler) (interface{}, error) {
			if md, ok := metadata.FromIncomingContext(ctx); ok {
				for _, auth := range md.Get("authorization") {
					if auth == "Bearer "+os.Getenv("TOKEN") {
						return handler(ctx, req)
					}
				}
			}
			return nil, status.Errorf(codes.PermissionDenied, "Not authorized")
		}),
	)

	if err := s.Serve(lis); err != nil {
		log.Fatalf("failed to serve: %v", err)
	}
}
