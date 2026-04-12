// Package lightning provides an interface for making Lightning Network payments.
// The default implementation connects to LND via its REST API.
package lightning

import (
	"crypto/tls"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"time"
)

// Client is the interface for sending Lightning payments.
type Client interface {
	PayAddress(address string, amountSats int64) (paymentHash string, err error)
	GetBalance() (sats int64, err error)
}

// LNDClient connects to an LND node via REST API.
type LNDClient struct {
	baseURL  string // e.g. "https://192.168.1.100:8080"
	macaroon string // hex-encoded macaroon
	client   *http.Client
}

// NewLND creates a connection to an LND REST API.
// addr: REST address (e.g. "192.168.1.100:8080")
// macaroonPath: path to admin.macaroon file
// tlsPath: path to tls.cert file (empty = skip TLS verify for Tailscale)
func NewLND(addr, macaroonPath, tlsPath string) (Client, error) {
	// Read macaroon
	macBytes, err := os.ReadFile(macaroonPath)
	if err != nil {
		return nil, fmt.Errorf("read macaroon: %w", err)
	}
	macHex := hex.EncodeToString(macBytes)

	// TLS config
	tlsConfig := &tls.Config{InsecureSkipVerify: true} //nolint:gosec // Umbrel self-signed cert over Tailscale
	if tlsPath != "" {
		// TODO: load custom CA cert if provided
		_ = tlsPath
	}

	scheme := "https"
	if !strings.Contains(addr, ":") {
		addr += ":8080"
	}

	return &LNDClient{
		baseURL:  scheme + "://" + addr,
		macaroon: macHex,
		client: &http.Client{
			Timeout:   30 * time.Second,
			Transport: &http.Transport{TLSClientConfig: tlsConfig},
		},
	}, nil
}

func (c *LNDClient) do(method, path string, body io.Reader) (*http.Response, error) {
	req, err := http.NewRequest(method, c.baseURL+path, body)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Grpc-Metadata-macaroon", c.macaroon)
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	return c.client.Do(req)
}

// GetBalance returns the local channel balance in sats.
func (c *LNDClient) GetBalance() (int64, error) {
	resp, err := c.do("GET", "/v1/balance/channels", nil)
	if err != nil {
		return 0, fmt.Errorf("lnd balance: %w", err)
	}
	defer resp.Body.Close()

	var data struct {
		LocalBalance struct {
			Sat string `json:"sat"`
		} `json:"local_balance"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&data); err != nil {
		return 0, err
	}
	var sats int64
	fmt.Sscanf(data.LocalBalance.Sat, "%d", &sats)
	return sats, nil
}

// PayAddress sends sats to a Lightning address (user@domain.com).
// Flow: resolve LNURL → fetch invoice → pay invoice.
func (c *LNDClient) PayAddress(address string, amountSats int64) (string, error) {
	// Step 1: Resolve Lightning address to LNURL callback
	parts := strings.SplitN(address, "@", 2)
	if len(parts) != 2 {
		return "", fmt.Errorf("invalid lightning address: %s", address)
	}
	user, domain := parts[0], parts[1]
	lnurlURL := fmt.Sprintf("https://%s/.well-known/lnurlp/%s", domain, user)

	httpClient := &http.Client{Timeout: 10 * time.Second}
	resp, err := httpClient.Get(lnurlURL)
	if err != nil {
		return "", fmt.Errorf("lnurl resolve: %w", err)
	}
	defer resp.Body.Close()

	var lnurl struct {
		Callback    string `json:"callback"`
		MinSendable int64  `json:"minSendable"` // millisats
		MaxSendable int64  `json:"maxSendable"` // millisats
	}
	if err := json.NewDecoder(resp.Body).Decode(&lnurl); err != nil {
		return "", fmt.Errorf("lnurl parse: %w", err)
	}

	amountMsats := amountSats * 1000
	if amountMsats < lnurl.MinSendable || amountMsats > lnurl.MaxSendable {
		return "", fmt.Errorf("amount %d sats outside range [%d-%d] msats", amountSats, lnurl.MinSendable, lnurl.MaxSendable)
	}

	// Step 2: Fetch invoice from callback
	sep := "?"
	if strings.Contains(lnurl.Callback, "?") {
		sep = "&"
	}
	invoiceURL := fmt.Sprintf("%s%samount=%d", lnurl.Callback, sep, amountMsats)
	resp2, err := httpClient.Get(invoiceURL)
	if err != nil {
		return "", fmt.Errorf("fetch invoice: %w", err)
	}
	defer resp2.Body.Close()

	var inv struct {
		PR string `json:"pr"` // bolt11 invoice
	}
	if err := json.NewDecoder(resp2.Body).Decode(&inv); err != nil {
		return "", fmt.Errorf("invoice parse: %w", err)
	}
	if inv.PR == "" {
		return "", fmt.Errorf("empty invoice from %s", address)
	}

	// Step 3: Pay the invoice via LND
	return c.payInvoice(inv.PR)
}

func (c *LNDClient) payInvoice(bolt11 string) (string, error) {
	payload := fmt.Sprintf(`{"payment_request":"%s","timeout_seconds":30,"fee_limit":{"fixed":"50"}}`, bolt11)
	resp, err := c.do("POST", "/v1/channels/transactions", strings.NewReader(payload))
	if err != nil {
		return "", fmt.Errorf("lnd pay: %w", err)
	}
	defer resp.Body.Close()

	body, _ := io.ReadAll(resp.Body)
	var result struct {
		PaymentHash  string `json:"payment_hash"`
		PaymentError string `json:"payment_error"`
	}
	if err := json.Unmarshal(body, &result); err != nil {
		return "", fmt.Errorf("lnd pay parse: %w", err)
	}
	if result.PaymentError != "" {
		return "", fmt.Errorf("lnd: %s", result.PaymentError)
	}
	return result.PaymentHash, nil
}
