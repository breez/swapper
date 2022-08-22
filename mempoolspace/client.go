package mempoolspace

import (
	"bytes"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io/ioutil"
	"net/http"

	"github.com/btcsuite/btcd/btcutil"
	"github.com/btcsuite/btcd/chaincfg/chainhash"
	"github.com/btcsuite/btcd/wire"
)

type Client struct {
	// meempool parameters
	BaseUrl string // need to implement
}
type Utxo struct {
	Value       btcutil.Amount
	BlockHeight int32
	wire.OutPoint
}
type respUtxo struct {
	txid   []byte
	vout   int
	status status
	value  btcutil.Amount
}
type status struct {
	confirmed    bool
	block_height int32
	block_hash   []byte
	block_time   uint64
}
type RecommendedFeesResponse struct {
	minimumFee uint64
}
type BroadcastTransactionResponse struct {
	txid []byte
}

func (c *Client) RecommendedFee() (uint64, error) {
	response, err := http.Get(c.BaseUrl + "/v1/fees/recommended")
	responseBody, err := ioutil.ReadAll(response.Body)
	if err != nil {
		fmt.Print(err.Error())
	}
	defer response.Body.Close()
	var recommendedFeesResponse RecommendedFeesResponse
	json.Unmarshal(responseBody, &recommendedFeesResponse)

	return recommendedFeesResponse.minimumFee, err
}
func (c *Client) GetUtxos(hash []byte) ([]Utxo, error) {
	response, err := http.Get(c.BaseUrl + "/address/" + hex.EncodeToString(hash) + "/utxo")
	if err != nil {
		return nil, err
	}
	responseBody, err := ioutil.ReadAll(response.Body)
	defer response.Body.Close()

	var respUtxo []respUtxo
	json.Unmarshal(responseBody, &respUtxo)
	var txos []Utxo
	outPoints := make(map[string]struct{})

	for i, d := range respUtxo {
		if d.status.confirmed == true {
			resp, err := http.Get(c.BaseUrl + "/tx/" + hex.EncodeToString(d.txid) + "/hex")
			if err != nil {
				return nil, err
			}
			txHash, _ := ioutil.ReadAll(resp.Body)
			newhash, err := chainhash.NewHash(txHash)
			op := wire.NewOutPoint(newhash, uint32(i))
			txos = append(txos, Utxo{
				Value:       d.value,
				BlockHeight: d.status.block_height,
				OutPoint:    *op,
			})
			outPoints[op.String()] = struct{}{}
		}
	}
	return txos, nil
}
func (c *Client) BroadcastTransaction(redeemTx *wire.MsgTx) ([]byte, error) {
	// Serialize the transaction.
	var buf bytes.Buffer
	err := redeemTx.Serialize(&buf)
	if err != nil {
		return nil, err
	}
	resp, err := http.Post(c.BaseUrl+"/tx", "application/json", &buf)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	body, err := ioutil.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}
	return body, nil
}
func (c *Client) CurrentHeight() (uint32, error) {
	response, err := http.Get(c.BaseUrl + "/blocks/tip/height")
	if err != nil {
		return 0, err
	}
	responseBody, _ := ioutil.ReadAll(response.Body)
	defer response.Body.Close()

	return binary.BigEndian.Uint32(responseBody), err
}
