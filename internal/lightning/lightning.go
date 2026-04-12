// Package lightning provides an interface for making Lightning Network payments.
// The default implementation connects to LND via gRPC.
package lightning

// Client is the interface for sending Lightning payments.
// Implementations: LND (production), Mock (testing).
type Client interface {
	// PayInvoice pays a Lightning invoice (bolt11).
	PayInvoice(invoice string) (paymentHash string, err error)

	// PayAddress sends sats to a Lightning address (user@domain.com).
	// The implementation resolves the LNURL, fetches an invoice, and pays it.
	PayAddress(address string, amountSats int64) (paymentHash string, err error)

	// GetBalance returns the local channel balance in sats.
	GetBalance() (sats int64, err error)
}

// LNDClient connects to an LND node via gRPC.
type LNDClient struct {
	addr     string
	macaroon []byte
	tlsCert  []byte
}

// NewLND creates a connection to an LND node.
// addr: gRPC address (e.g. "192.168.1.100:10009")
// macaroonPath: path to admin.macaroon
// tlsPath: path to tls.cert
func NewLND(addr, macaroonPath, tlsPath string) (Client, error) {
	// TODO: read macaroon and TLS cert files, establish gRPC connection
	return &LNDClient{addr: addr}, nil
}

func (c *LNDClient) PayInvoice(invoice string) (string, error) {
	// TODO: call lnrpc.SendPaymentSync
	return "", nil
}

func (c *LNDClient) PayAddress(address string, amountSats int64) (string, error) {
	// TODO: resolve LNURL from Lightning address, fetch invoice, pay it
	return "", nil
}

func (c *LNDClient) GetBalance() (int64, error) {
	// TODO: call lnrpc.ChannelBalance
	return 0, nil
}
