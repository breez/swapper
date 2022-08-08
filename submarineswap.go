package main

import (
	"crypto/sha256"
	"crypto/x509"
	"errors"
	"log"
	"net"
	"os"
	"strings"
	"swapper/mempoolspace"

	"github.com/btcsuite/btcd/btcec"
	"github.com/btcsuite/btcd/chaincfg"
	"github.com/btcsuite/btcd/txscript"
	"github.com/btcsuite/btcd/wire"
	"github.com/btcsuite/btcutil"
	"github.com/lightningnetwork/lnd/input"
	"github.com/lightningnetwork/lnd/lnwallet/chainfee"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials"
)

const (
	defaultLockHeight      = 288
	redeemWitnessInputSize = 1 + 1 + 73 + 1 + 32 + 1 + 100
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
func redeemFees(net *chaincfg.Params, hash []byte, feePerKw chainfee.SatPerKWeight) (btcutil.Amount, error) {
	c := &mempoolspace.Client{baseUrl: "https://mempool.space/api"}
	utxos, err := c.GetUtxos(hash)
	if err != nil {
		return 0, err
	}
	if len(utxos) == 0 {
		return 0, errors.New("no utxo")
	}

	redeemTx := wire.NewMsgTx(1)

	// Add the inputs without the witness and calculate the amount to redeem
	var amount btcutil.Amount
	for _, utxo := range utxos {
		amount += utxo.Value
		txIn := wire.NewTxIn(&utxo.OutPoint, nil, nil)
		txIn.Sequence = 0
		redeemTx.AddTxIn(txIn)
	}

	//Generate a random address
	privateKey, err := btcec.NewPrivateKey(btcec.S256())
	if err != nil {
		return 0, err
	}
	redeemAddress, err := btcutil.NewAddressPubKey(privateKey.PubKey().SerializeCompressed(), net)
	if err != nil {
		return 0, err
	}
	// Add the single output
	redeemScript, err := txscript.PayToAddrScript(redeemAddress)
	if err != nil {
		return 0, err
	}
	txOut := wire.TxOut{PkScript: redeemScript}
	redeemTx.AddTxOut(&txOut)

	currentHeight, err := c.CurrentHeight()
	if err != nil {
		return 0, err
	}
	redeemTx.LockTime = uint32(currentHeight)

	// Calcluate the weight and the fee
	weight := 4*redeemTx.SerializeSizeStripped() + redeemWitnessInputSize*len(redeemTx.TxIn)
	// Adjust the amount in the txout
	return feePerKw.FeeForWeight(int64(weight)), nil
}

// Redeem
func redeem(net *chaincfg.Params, preimage []byte, redeemAddress btcutil.Address, feePerKw chainfee.SatPerKWeight) (*wire.MsgTx, error) {
	c := &mempoolspace.Client{baseUrl: "https://mempool.space/api"}
	hash := sha256.Sum256(preimage)
	_, serviceKey, script, err := getSwapperSubmarineData(net.ScriptHashAddrID, hash[:])
	if err != nil {
		return nil, err
	}
	utxos, err := c.GetUtxos(hash)
	if err != nil {
		return 0, err
	}
	if len(utxos) == 0 {
		return 0, errors.New("no utxo")
	}

	redeemTx := wire.NewMsgTx(1)

	// Add the inputs without the witness and calculate the amount to redeem
	var amount btcutil.Amount
	for _, utxo := range utxos {
		amount += utxo.Value
		txIn := wire.NewTxIn(&utxo.OutPoint, nil, nil)
		txIn.Sequence = 0
		redeemTx.AddTxIn(txIn)
	}

	// Add the single output
	redeemScript, err := txscript.PayToAddrScript(redeemAddress)
	if err != nil {
		return nil, err
	}
	txOut := wire.TxOut{PkScript: redeemScript}
	redeemTx.AddTxOut(&txOut)

	currentHeight, err := c.CurrentHeight()
	if err != nil {
		return 0, err
	}
	redeemTx.LockTime = uint32(currentHeight)

	// Calcluate the weight and the fee
	weight := 4*redeemTx.SerializeSizeStripped() + redeemWitnessInputSize*len(redeemTx.TxIn)
	// Adjust the amount in the txout
	redeemTx.TxOut[0].Value = int64(amount - feePerKw.FeeForWeight(int64(weight)))

	sigHashes := txscript.NewTxSigHashes(redeemTx)
	privateKey, _ := btcec.PrivKeyFromBytes(btcec.S256(), serviceKey)
	for idx := range redeemTx.TxIn {
		scriptSig, err := txscript.RawTxInWitnessSignature(redeemTx, sigHashes, idx, int64(utxos[idx].Value), script, txscript.SigHashAll, privateKey)
		if err != nil {
			return nil, err
		}
		redeemTx.TxIn[idx].Witness = [][]byte{scriptSig, preimage, script}
	}

	err = c.BroadcastTransaction(redeemTx)
	if err != nil {
		return nil, err
	}

	return redeemTx, nil
}

func subSwapServiceRedeemFees(ActiveNetParams *chaincfg.Params, hash []byte) (int64, error) {
	c := &mempoolspace.Client{baseUrl: "https://mempool.space/api"}
	fee, err := c.RecommendedFee()
	feePerKw, err := chainfee.SatPerKVByte(fee * 1000).FeePerKWeight()
	if err != nil {
		return 0, err
	}

	amount, err := redeemFees(ActiveNetParams, hash, feePerKw)

	if err != nil {
		return 0, err
	}
	return int64(amount), nil
}
func subSwapServiceRedeem(ActiveNetParams *chaincfg.Params, hash []byte, preimage []byte, redeemAddress btcutil.Address) ([]byte, error) {
	c := &mempoolspace.Client{baseUrl: "https://mempool.space/api"}
	fee, err := c.RecommendedFee()
	feePerKw, err := chainfee.SatPerKVByte(fee * 1000).FeePerKWeight()
	if err != nil {
		return nil, err
	}
	tx, err := redeem(
		ActiveNetParams,
		preimage,
		redeemAddress,
		feePerKw,
	)

	if err != nil {
		return nil, err
	}
	log.Infof("[subswapserviceredeem] txid: %v", tx.TxHash().String())
	return tx.TxHash().String(), nil
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
	var opts []grpc.ServerOption
	s := grpc.NewServer(opts...)

	if err := s.Serve(lis); err != nil {
		log.Fatalf("failed to serve: %v", err)
	}
}
