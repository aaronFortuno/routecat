// Package lightning provides an interface for making Lightning Network payments.
// The default implementation connects to LND via its REST API.
package lightning

import (
	"crypto/tls"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"strings"
	"time"
)

// Client is the interface for Lightning operations.
type Client interface {
	PayAddress(address string, amountSats int64) (paymentHash string, err error)
	GetBalance() (sats int64, err error)
	CreateInvoice(amountSats int64, memo string) (bolt11 string, paymentHash string, err error)
	CheckInvoice(paymentHash string) (paid bool, err error)
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

	log.Printf("routecat: [lnurl] step 1 — resolving %s", lnurlURL)
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
	log.Printf("routecat: [lnurl] step 1 OK — callback=%s min=%d max=%d", lnurl.Callback, lnurl.MinSendable, lnurl.MaxSendable)

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
	log.Printf("routecat: [lnurl] step 2 — fetching invoice from %s", invoiceURL)
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
	log.Printf("routecat: [lnurl] step 3 — paying invoice (%d chars)", len(inv.PR))
	hash, err := c.payInvoice(inv.PR)
	log.Printf("routecat: [lnurl] step 3 result — hash=%q err=%v", hash, err)
	return hash, err
}

// CreateInvoice generates a Lightning invoice via LND.
func (c *LNDClient) CreateInvoice(amountSats int64, memo string) (string, string, error) {
	payload := fmt.Sprintf(`{"value":"%d","memo":"%s","expiry":"600"}`, amountSats, memo)
	resp, err := c.do("POST", "/v1/invoices", strings.NewReader(payload))
	if err != nil {
		return "", "", fmt.Errorf("lnd invoice: %w", err)
	}
	defer resp.Body.Close()

	var result struct {
		PaymentRequest string `json:"payment_request"`
		RHash          string `json:"r_hash"` // base64-encoded
	}
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return "", "", fmt.Errorf("lnd invoice parse: %w", err)
	}

	payHash := b64ToHex(result.RHash)
	return result.PaymentRequest, payHash, nil
}

// CheckInvoice checks if a Lightning invoice has been paid.
func (c *LNDClient) CheckInvoice(paymentHash string) (bool, error) {
	// LND REST accepts the hex hash directly in the URL path
	resp, err := c.do("GET", "/v1/invoice/"+paymentHash, nil)
	if err != nil {
		return false, fmt.Errorf("lnd lookup: %w", err)
	}
	defer resp.Body.Close()

	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != 200 {
		return false, fmt.Errorf("lnd lookup HTTP %d: %s", resp.StatusCode, string(body[:min(len(body), 100)]))
	}

	var result struct {
		Settled bool   `json:"settled"`
		State   string `json:"state"`
	}
	if err := json.Unmarshal(body, &result); err != nil {
		return false, err
	}
	return result.Settled || result.State == "SETTLED", nil
}

func b64ToHex(s string) string {
	b, err := base64.RawURLEncoding.DecodeString(s)
	if err != nil {
		b, _ = base64.StdEncoding.DecodeString(s)
	}
	return hex.EncodeToString(b)
}

func hexToB64URL(s string) string {
	b, _ := hex.DecodeString(s)
	return base64.RawURLEncoding.EncodeToString(b)
}

func (c *LNDClient) payInvoice(bolt11 string) (string, error) {
	payload := fmt.Sprintf(`{"payment_request":"%s","fee_limit":{"fixed":"50"}}`, bolt11)
	resp, err := c.do("POST", "/v1/channels/transactions", strings.NewReader(payload))
	if err != nil {
		return "", fmt.Errorf("lnd pay: %w", err)
	}
	defer resp.Body.Close()

	body, _ := io.ReadAll(resp.Body)
	log.Printf("routecat: [lnd] sendpayment response (status %d): %s", resp.StatusCode, string(body[:min(len(body), 500)]))

	if resp.StatusCode != 200 {
		return "", fmt.Errorf("lnd pay HTTP %d: %s", resp.StatusCode, string(body[:min(len(body), 200)]))
	}

	var result struct {
		PaymentHash  string `json:"payment_hash"`
		PaymentError string `json:"payment_error"`
	}
	if err := json.Unmarshal(body, &result); err != nil {
		return "", fmt.Errorf("lnd pay parse: %w — body: %s", err, string(body[:min(len(body), 200)]))
	}
	if result.PaymentError != "" {
		return "", fmt.Errorf("lnd: %s", result.PaymentError)
	}
	// payment_hash from LND is base64-encoded — convert to hex
	if result.PaymentHash != "" {
		return b64ToHex(result.PaymentHash), nil
	}
	return "", nil
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}
