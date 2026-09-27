package kraken

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"time"

	"ts3/internal/source/wsclient"
)

const Endpoint = "wss://ws.kraken.com/v2"
const ProductURL = "https://api.kraken.com/0/public/AssetPairs?pair=XBTUSD"
const Symbol = "BTC/USD"
const Depth = 100

func FetchMetadata(ctx context.Context, client *http.Client) ([]byte, time.Time, time.Time, error) {
	if client == nil {
		client = &http.Client{Timeout: 10 * time.Second}
	}
	start := time.Now().UTC()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, ProductURL, nil)
	if err != nil {
		return nil, start, time.Time{}, err
	}
	resp, err := client.Do(req)
	if err != nil {
		return nil, start, time.Now().UTC(), err
	}
	defer resp.Body.Close()
	b, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	end := time.Now().UTC()
	if err != nil {
		return nil, start, end, err
	}
	if resp.StatusCode != http.StatusOK {
		return b, start, end, fmt.Errorf("product HTTP %d", resp.StatusCode)
	}
	var v struct {
		Error  []string `json:"error"`
		Result map[string]struct {
			Altname string `json:"altname"`
			Status  string `json:"status"`
		} `json:"result"`
	}
	if err := json.Unmarshal(b, &v); err != nil {
		return b, start, end, err
	}
	if len(v.Error) != 0 {
		return b, start, end, fmt.Errorf("Kraken product error: %v", v.Error)
	}
	for _, pair := range v.Result {
		if pair.Altname == "XBTUSD" && pair.Status == "online" {
			return b, start, end, nil
		}
	}
	return b, start, end, errors.New("BTC/USD product unavailable")
}

func Subscribe(c *wsclient.Conn) error {
	return c.SendText([]byte(`{"method":"subscribe","params":{"channel":"book","symbol":["BTC/USD"],"depth":100,"snapshot":true},"req_id":1}`))
}
