package bmc

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"errors"
	"fmt"
	"math/big"
	"net"
	"os"
	"path/filepath"
	"slices"
	"time"
)

const (
	caValidity   = 10 * 365 * 24 * time.Hour
	certValidity = 825 * 24 * time.Hour
	// renewBefore is how long before expiry `up` replaces a certificate.
	renewBefore = 30 * 24 * time.Hour
)

// CA signs the certificates of the host's Redfish BMCs, so clients trust
// one file (CACertPath) for every BMC of every lab.
type CA struct {
	Cert *x509.Certificate
	Key  *ecdsa.PrivateKey
	// CertFile is the PEM certificate clients should trust.
	CertFile string
}

func CACertPath(dir string) string { return filepath.Join(dir, "ca.crt") }
func caKeyPath(dir string) string  { return filepath.Join(dir, "ca.key") }
func certPath(dir string) string   { return filepath.Join(dir, "tls.crt") }
func keyPath(dir string) string    { return filepath.Join(dir, "tls.key") }

// EnsureCA loads the CA kept in dir, or creates it. A CA close to expiry
// is replaced, which makes every BMC certificate be reissued.
func EnsureCA(dir string, w FileWriter) (*CA, error) {
	ca := &CA{CertFile: CACertPath(dir)}
	cert, key, err := loadPair(ca.CertFile, caKeyPath(dir))
	if err == nil && cert.IsCA && time.Until(cert.NotAfter) > renewBefore {
		ca.Cert, ca.Key = cert, key
		return ca, nil
	}
	key, err = ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return nil, err
	}
	host, _ := os.Hostname()
	tmpl := &x509.Certificate{
		SerialNumber:          serial(),
		Subject:               pkix.Name{CommonName: "bmbox BMC CA " + host, Organization: []string{"bmbox"}},
		NotBefore:             time.Now().Add(-time.Hour),
		NotAfter:              time.Now().Add(caValidity),
		IsCA:                  true,
		BasicConstraintsValid: true,
		MaxPathLenZero:        true,
		KeyUsage:              x509.KeyUsageCertSign | x509.KeyUsageCRLSign,
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &key.PublicKey, key)
	if err != nil {
		return nil, err
	}
	if err := writePair(w, ca.CertFile, caKeyPath(dir), der, key); err != nil {
		return nil, fmt.Errorf("write BMC CA: %w", err)
	}
	ca.Cert, _ = x509.ParseCertificate(der)
	ca.Key = key
	return ca, nil
}

// certIPs are the addresses a BMC listening on addr is reached at: addr
// itself, or every address of the host for a wildcard address.
func certIPs(addr string) ([]net.IP, []string) {
	ip := net.ParseIP(addr)
	if ip != nil && !ip.IsUnspecified() {
		return []net.IP{ip}, nil
	}
	ips := []net.IP{net.IPv4(127, 0, 0, 1), net.IPv6loopback}
	if addrs, err := net.InterfaceAddrs(); err == nil {
		for _, a := range addrs {
			if n, ok := a.(*net.IPNet); ok && !n.IP.IsLoopback() && !n.IP.IsLinkLocalUnicast() {
				ips = append(ips, n.IP)
			}
		}
	}
	names := []string{"localhost"}
	if host, err := os.Hostname(); err == nil {
		names = append(names, host)
	}
	return ips, names
}

// ensureCert makes sure dir holds a certificate for addr signed by ca and
// not close to expiry. It reports whether a new one was written.
func ensureCert(dir, addr string, ca *CA, w FileWriter) (bool, error) {
	ips, names := certIPs(addr)
	if cert, _, err := loadPair(certPath(dir), keyPath(dir)); err == nil && certValid(cert, ca, ips) {
		return false, nil
	}
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return false, err
	}
	tmpl := &x509.Certificate{
		SerialNumber: serial(),
		Subject:      pkix.Name{CommonName: "bmbox BMC " + addr, Organization: []string{"bmbox"}},
		NotBefore:    time.Now().Add(-time.Hour),
		NotAfter:     time.Now().Add(certValidity),
		KeyUsage:     x509.KeyUsageDigitalSignature,
		ExtKeyUsage:  []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
		IPAddresses:  ips,
		DNSNames:     names,
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, ca.Cert, &key.PublicKey, ca.Key)
	if err != nil {
		return false, err
	}
	if err := writePair(w, certPath(dir), keyPath(dir), der, key); err != nil {
		return false, fmt.Errorf("write BMC certificate: %w", err)
	}
	return true, nil
}

func certValid(cert *x509.Certificate, ca *CA, ips []net.IP) bool {
	if time.Until(cert.NotAfter) < renewBefore || cert.CheckSignatureFrom(ca.Cert) != nil {
		return false
	}
	for _, ip := range ips {
		if !slices.ContainsFunc(cert.IPAddresses, ip.Equal) {
			return false
		}
	}
	return true
}

func loadPair(certFile, keyFile string) (*x509.Certificate, *ecdsa.PrivateKey, error) {
	pair, err := tls.LoadX509KeyPair(certFile, keyFile)
	if err != nil {
		return nil, nil, err
	}
	cert, err := x509.ParseCertificate(pair.Certificate[0])
	if err != nil {
		return nil, nil, err
	}
	key, ok := pair.PrivateKey.(*ecdsa.PrivateKey)
	if !ok {
		return nil, nil, errors.New("not an ECDSA key")
	}
	return cert, key, nil
}

func writePair(w FileWriter, certFile, keyFile string, der []byte, key *ecdsa.PrivateKey) error {
	kb, err := x509.MarshalECPrivateKey(key)
	if err != nil {
		return err
	}
	if err := w.WriteSecret(keyFile, pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: kb})); err != nil {
		return err
	}
	return w.WriteFile(certFile, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}))
}

func serial() *big.Int {
	n, _ := rand.Int(rand.Reader, new(big.Int).Lsh(big.NewInt(1), 127))
	return n
}
