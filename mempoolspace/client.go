package mempoolspace

import (
	"encoding/json"
	"fmt"
	"io/ioutil"
	"net/http"
)

type Client struct {
	// meempool parameters
	minimumFee int64
}

func recommendedFee() (*Client, error) {
	response, err := http.Get("https://mempool.space/api/v1/fees/recommended")
	responseBody, err := ioutil.ReadAll(response.Body)
	if err != nil {
		fmt.Print(err.Error())
	}
	var client Client
	json.Unmarshal(responseBody, &client)

	return &Client{
		minimumFee: client.minimumFee,
	}, err
}
