package bmc

import (
	"crypto/tls"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

func TestCAIsCreatedOnceAndReused(t *testing.T) {
	dir := t.TempDir()
	w := &dirWriter{secrets: map[string]bool{}}
	ca, err := EnsureCA(dir, w)
	if err != nil || !ca.Cert.IsCA || !w.secrets["ca.key"] {
		t.Fatalf("EnsureCA: %v, secrets=%v", err, w.secrets)
	}
	again, err := EnsureCA(dir, w)
	if err != nil || !again.Cert.Equal(ca.Cert) {
		t.Fatalf("second EnsureCA made a new CA: %v", err)
	}
}

func TestCertIsReissuedOnlyWhenNeeded(t *testing.T) {
	caDir, dir := t.TempDir(), t.TempDir()
	w := &dirWriter{secrets: map[string]bool{}}
	ca, _ := EnsureCA(caDir, w)

	if renewed, err := ensureCert(dir, "127.0.0.1", ca, w); err != nil || !renewed {
		t.Fatalf("first cert: renewed=%v err=%v", renewed, err)
	}
	if renewed, _ := ensureCert(dir, "127.0.0.1", ca, w); renewed {
		t.Error("valid cert was reissued")
	}
	if renewed, _ := ensureCert(dir, "192.0.2.10", ca, w); !renewed {
		t.Error("cert not reissued for a new address")
	}
	other, _ := EnsureCA(t.TempDir(), w)
	if renewed, _ := ensureCert(dir, "192.0.2.10", other, w); !renewed {
		t.Error("cert not reissued for a new CA")
	}
}

func TestRedfishTLSConfigAndProbe(t *testing.T) {
	s := testSpec(t, TypeRedfish)
	w := &dirWriter{secrets: map[string]bool{}}
	ca, _ := EnsureCA(t.TempDir(), w)
	s.TLS, s.CA = true, ca
	if _, changed, err := writeRedfish(s, w, Tools{SushyEmulator: "/bin/sushy-emulator"}); err != nil || !changed {
		t.Fatalf("writeRedfish: changed=%v err=%v", changed, err)
	}
	cfg, _ := os.ReadFile(filepath.Join(s.Dir, "sushy.conf"))
	for _, want := range []string{"SUSHY_EMULATOR_SSL_CERT", "SUSHY_EMULATOR_SSL_KEY"} {
		if !strings.Contains(string(cfg), want) {
			t.Errorf("config missing %s", want)
		}
	}
	if !w.secrets["tls.key"] {
		t.Error("TLS key not written as a secret")
	}
	if !strings.HasPrefix(s.Endpoint(), "https://") {
		t.Errorf("endpoint %s is not https", s.Endpoint())
	}

	// Serve the certificate like sushy would and probe it with the CA.
	pair, err := tls.LoadX509KeyPair(certPath(s.Dir), keyPath(s.Dir))
	if err != nil {
		t.Fatal(err)
	}
	l, err := tls.Listen("tcp", "127.0.0.1:0", &tls.Config{Certificates: []tls.Certificate{pair}})
	if err != nil {
		t.Fatal(err)
	}
	defer l.Close()
	go http.Serve(l, http.HandlerFunc(func(rw http.ResponseWriter, r *http.Request) {
		if u, p, ok := r.BasicAuth(); !ok || u != s.Username || p != s.Password {
			rw.WriteHeader(http.StatusUnauthorized)
		}
	}))
	_, port, _ := net.SplitHostPort(l.Addr().String())
	s.Port, _ = strconv.Atoi(port)
	if err := probeRedfish(s); err != nil {
		t.Errorf("probe with the CA: %v", err)
	}
	s.CA, _ = EnsureCA(t.TempDir(), w)
	if err := probeRedfish(s); err == nil {
		t.Error("probe trusted a certificate from another CA")
	}
}
