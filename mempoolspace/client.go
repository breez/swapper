package mempoolspace

import (
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io/ioutil"
	"net/http"
)

type Client struct {
	// meempool parameters
	uri string // need to implement
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

func RecommendedFee() (uint64, error) {
	response, err := http.Get("https://mempool.space/api/v1/fees/recommended")
	responseBody, err := ioutil.ReadAll(response.Body)
	if err != nil {
		fmt.Print(err.Error())
	}
	var recommendedFeesResponse RecommendedFeesResponse
	json.Unmarshal(responseBody, &recommendedFeesResponse)

	return recommendedFeesResponse.minimumFee, err
}
func GetUtxos(hash []byte) ([]Utxo, error) {
	response, err := http.Get("https://mempool.space/api/address/" + hex.EncodeToString(hash) + "/utxo")
	if err != nil {
		return nil, err
	}
	responseBody, err := ioutil.ReadAll(response.Body)
	var respUtxo []respUtxo
	json.Unmarshal(responseBody, &respUtxo)
	var txos []Utxo
	outPoints := make(map[string]struct{})
	for i, d := range respUtxo {
		if d.status.confirmed == true {
			resp, err := http.Get("https://mempool.space/api/tx/" + hex.EncodeToString(d.txid) + "/hex")
			if err != nil {
				return nil, err
			}
			txHash, _ := ioutil.ReadAll(resp.Body)
			op := wire.NewOutPoint(&txHash, uint32(i))
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
func CurrentHeight() (uint32, error) {
	response, err := http.Get("https://mempool.space/api/blocks/tip/height")
	if err != nil {
		return 0, err
	}
	responseBody, _ := ioutil.ReadAll(response.Body)

	return binary.BigEndian.Uint32(responseBody), err
}
