package network

import (
	"crypto/ed25519"
	"crypto/rand"
	"crypto/sha256"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/hex"
	"encoding/pem"
	"io"
	"math/big"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

type testCertificateAuthority struct {
	certificate *x509.Certificate
	privateKey  ed25519.PrivateKey
	pem         []byte
}

type testCertificate struct {
	tlsCertificate tls.Certificate
	certificatePEM []byte
	privateKeyPEM  []byte
	leaf           *x509.Certificate
}

func TestCustomRootAndHTTP2Policy(t *testing.T) {
	server := httptest.NewUnstartedServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		_, _ = io.WriteString(writer, request.Proto)
	}))
	server.EnableHTTP2 = true
	server.StartTLS()
	defer server.Close()

	settings := DefaultSettings()
	settings.ProxyMode = ProxyModeDirect
	settings.CAFile = writeCertificateFile(t, server.Certificate())

	client := clientForSettings(t, settings)
	response, err := client.Get(server.URL)
	if err != nil {
		t.Fatalf("HTTP/2 request with custom root: %v", err)
	}
	body, err := io.ReadAll(response.Body)
	response.Body.Close()
	if err != nil {
		t.Fatal(err)
	}
	if string(body) != "HTTP/2.0" {
		t.Fatalf("default protocol = %q, want HTTP/2.0", body)
	}

	settings.DisableHTTP2 = true
	http1Client := clientForSettings(t, settings)
	response, err = http1Client.Get(server.URL)
	if err != nil {
		t.Fatalf("HTTP/1.1 request with HTTP/2 disabled: %v", err)
	}
	body, err = io.ReadAll(response.Body)
	response.Body.Close()
	if err != nil {
		t.Fatal(err)
	}
	if string(body) != "HTTP/1.1" {
		t.Fatalf("disabled protocol = %q, want HTTP/1.1", body)
	}
}

func TestClientCertificateIsExactHostAndNotSentAcrossRedirect(t *testing.T) {
	authority := newTestCertificateAuthority(t)
	serverCertificate := authority.issue(t, "server", []string{"localhost"}, []net.IP{net.ParseIP("127.0.0.1")}, []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth}, time.Now().Add(-time.Hour), time.Now().Add(time.Hour))
	clientCertificate := authority.issue(t, "client", nil, nil, []x509.ExtKeyUsage{x509.ExtKeyUsageClientAuth}, time.Now().Add(-time.Hour), time.Now().Add(time.Hour))
	clientCAPool := x509.NewCertPool()
	clientCAPool.AppendCertsFromPEM(authority.pem)

	var redirectedPeerCertificates atomic.Int32
	redirected := startTestTLSServer(t, serverCertificate.tlsCertificate, &tls.Config{
		ClientAuth: tls.RequestClientCert,
		ClientCAs:  clientCAPool,
	}, http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		redirectedPeerCertificates.Store(int32(len(request.TLS.PeerCertificates)))
		_, _ = io.WriteString(writer, "redirected")
	}))
	defer redirected.Close()
	redirectedURL := replaceURLHostname(t, redirected.URL, "localhost")

	var initialPeerCertificates atomic.Int32
	initial := startTestTLSServer(t, serverCertificate.tlsCertificate, &tls.Config{
		ClientAuth: tls.RequireAndVerifyClientCert,
		ClientCAs:  clientCAPool,
	}, http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		initialPeerCertificates.Store(int32(len(request.TLS.PeerCertificates)))
		http.Redirect(writer, request, redirectedURL, http.StatusFound)
	}))
	defer initial.Close()

	settings := DefaultSettings()
	settings.ProxyMode = ProxyModeDirect
	settings.CAFile = writePEMFile(t, "ca.pem", authority.pem)
	settings.ClientCertificateFile = writePEMFile(t, "client.pem", clientCertificate.certificatePEM)
	settings.ClientKeyFile = writePEMFile(t, "client.key", clientCertificate.privateKeyPEM)
	settings.ClientCertificateHost = mustURLHostname(t, initial.URL)
	if err := settings.ValidateFiles(); err != nil {
		t.Fatalf("ValidateFiles() error = %v", err)
	}
	client := clientForSettings(t, settings)

	response, err := client.Get(initial.URL)
	if err != nil {
		t.Fatalf("mTLS request with redirect: %v", err)
	}
	body, err := io.ReadAll(response.Body)
	response.Body.Close()
	if err != nil {
		t.Fatal(err)
	}
	if string(body) != "redirected" {
		t.Fatalf("body = %q", body)
	}
	if initialPeerCertificates.Load() != 1 {
		t.Fatalf("initial endpoint saw %d client certificates, want 1", initialPeerCertificates.Load())
	}
	if redirectedPeerCertificates.Load() != 0 {
		t.Fatalf("redirected endpoint saw %d client certificates, want 0", redirectedPeerCertificates.Load())
	}
}

func TestClientCertificateIsNeverSentToHTTPSProxy(t *testing.T) {
	authority := newTestCertificateAuthority(t)
	serverCertificate := authority.issue(t, "server", []string{"localhost"}, []net.IP{net.ParseIP("127.0.0.1")}, []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth}, time.Now().Add(-time.Hour), time.Now().Add(time.Hour))
	clientCertificate := authority.issue(t, "client", nil, nil, []x509.ExtKeyUsage{x509.ExtKeyUsageClientAuth}, time.Now().Add(-time.Hour), time.Now().Add(time.Hour))
	clientCAPool := x509.NewCertPool()
	clientCAPool.AppendCertsFromPEM(authority.pem)

	var targetPeerCertificates atomic.Int32
	target := startTestTLSServer(t, serverCertificate.tlsCertificate, &tls.Config{
		ClientAuth: tls.RequireAndVerifyClientCert,
		ClientCAs:  clientCAPool,
	}, http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		targetPeerCertificates.Store(int32(len(request.TLS.PeerCertificates)))
		_, _ = io.WriteString(writer, "mTLS target")
	}))
	defer target.Close()

	var proxyPeerCertificates atomic.Int32
	proxy := startTestTLSServer(t, serverCertificate.tlsCertificate, &tls.Config{
		ClientAuth: tls.RequestClientCert,
		ClientCAs:  clientCAPool,
	}, http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		proxyPeerCertificates.Store(int32(len(request.TLS.PeerCertificates)))
		if request.Method != http.MethodConnect {
			http.Error(writer, "CONNECT required", http.StatusMethodNotAllowed)
			return
		}
		tunnelHTTPConnect(t, writer, request, request.Host)
	}))
	defer proxy.Close()

	settings := DefaultSettings()
	settings.ProxyMode = ProxyModeCustom
	settings.ProxyURL = proxy.URL
	settings.CAFile = writePEMFile(t, "ca.pem", authority.pem)
	settings.ClientCertificateFile = writePEMFile(t, "client.pem", clientCertificate.certificatePEM)
	settings.ClientKeyFile = writePEMFile(t, "client.key", clientCertificate.privateKeyPEM)
	settings.ClientCertificateHost = mustURLHostname(t, target.URL)
	client := clientForSettings(t, settings)

	response, err := client.Get(target.URL)
	if err != nil {
		t.Fatalf("mTLS request through HTTPS proxy: %v", err)
	}
	response.Body.Close()
	if targetPeerCertificates.Load() != 1 {
		t.Fatalf("target saw %d client certificates, want 1", targetPeerCertificates.Load())
	}
	if proxyPeerCertificates.Load() != 0 {
		t.Fatalf("HTTPS proxy saw %d client certificates, want 0", proxyPeerCertificates.Load())
	}
}

func TestExactLeafPinAllowsInvalidCertificateOnlyForMatchingHost(t *testing.T) {
	now := time.Now()
	certificate := newSelfSignedServerCertificate(t, now.Add(-48*time.Hour), now.Add(-24*time.Hour))
	server := startTestTLSServer(t, certificate.tlsCertificate, nil, http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		_, _ = io.WriteString(writer, "pinned")
	}))
	defer server.Close()

	fingerprint := sha256.Sum256(certificate.leaf.Raw)
	settings := DefaultSettings()
	settings.ProxyMode = ProxyModeDirect
	settings.TrustedCertificateHost = mustURLHostname(t, server.URL)
	settings.TrustedCertificateSHA256 = hex.EncodeToString(fingerprint[:])
	client := clientForSettings(t, settings)

	response, err := client.Get(server.URL)
	if err != nil {
		t.Fatalf("request with exact expired self-signed pin: %v", err)
	}
	response.Body.Close()

	unrelatedURL := replaceURLHostname(t, server.URL, "localhost")
	_, err = client.Get(unrelatedURL)
	if err == nil {
		t.Fatal("same invalid certificate was trusted for an unrelated hostname")
	}

	mismatch := settings
	mismatch.TrustedCertificateSHA256 = strings.Repeat("00", sha256.Size)
	mismatchClient := clientForSettings(t, mismatch)
	_, err = mismatchClient.Get(server.URL)
	if err == nil || !strings.Contains(err.Error(), "pin does not match") {
		t.Fatalf("mismatched pin error = %v", err)
	}
}

func TestCertificatePinIsNeverAppliedToHTTPSProxy(t *testing.T) {
	certificate := newSelfSignedServerCertificate(t, time.Now().Add(-time.Hour), time.Now().Add(time.Hour))
	target := startTestTLSServer(t, certificate.tlsCertificate, nil, http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		_, _ = io.WriteString(writer, "target")
	}))
	defer target.Close()

	var proxyRequests atomic.Int32
	proxy := startTestTLSServer(t, certificate.tlsCertificate, nil, http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		proxyRequests.Add(1)
		tunnelHTTPConnect(t, writer, request, request.Host)
	}))
	defer proxy.Close()

	fingerprint := sha256.Sum256(certificate.leaf.Raw)
	settings := DefaultSettings()
	settings.ProxyMode = ProxyModeCustom
	settings.ProxyURL = proxy.URL
	settings.TrustedCertificateHost = mustURLHostname(t, target.URL)
	settings.TrustedCertificateSHA256 = hex.EncodeToString(fingerprint[:])
	client := clientForSettings(t, settings)

	_, err := client.Get(target.URL)
	if err == nil {
		t.Fatal("HTTPS proxy was incorrectly trusted by the target certificate pin")
	}
	if proxyRequests.Load() != 0 {
		t.Fatalf("untrusted HTTPS proxy received %d HTTP requests", proxyRequests.Load())
	}
}

func TestTLSMinimumVersionIsEnforced(t *testing.T) {
	server := httptest.NewUnstartedServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		writer.WriteHeader(http.StatusOK)
	}))
	server.TLS = &tls.Config{
		MinVersion: tls.VersionTLS12,
		MaxVersion: tls.VersionTLS12,
	}
	server.StartTLS()
	defer server.Close()

	settings := DefaultSettings()
	settings.ProxyMode = ProxyModeDirect
	settings.CAFile = writeCertificateFile(t, server.Certificate())
	settings.TLSMinimumVersion = TLSMinimumVersion13
	client := clientForSettings(t, settings)
	if _, err := client.Get(server.URL); err == nil {
		t.Fatal("TLS 1.3 minimum connected to a TLS 1.2-only server")
	}
}

func newTestCertificateAuthority(t *testing.T) testCertificateAuthority {
	t.Helper()
	publicKey, privateKey, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	template := &x509.Certificate{
		SerialNumber:          big.NewInt(1),
		Subject:               pkix.Name{CommonName: "Crude network test CA"},
		NotBefore:             time.Now().Add(-time.Hour),
		NotAfter:              time.Now().Add(24 * time.Hour),
		IsCA:                  true,
		BasicConstraintsValid: true,
		KeyUsage:              x509.KeyUsageCertSign | x509.KeyUsageDigitalSignature,
	}
	der, err := x509.CreateCertificate(rand.Reader, template, template, publicKey, privateKey)
	if err != nil {
		t.Fatal(err)
	}
	certificate, err := x509.ParseCertificate(der)
	if err != nil {
		t.Fatal(err)
	}
	return testCertificateAuthority{
		certificate: certificate,
		privateKey:  privateKey,
		pem: pem.EncodeToMemory(&pem.Block{
			Type:  "CERTIFICATE",
			Bytes: der,
		}),
	}
}

func (authority testCertificateAuthority) issue(
	t *testing.T,
	commonName string,
	dnsNames []string,
	ipAddresses []net.IP,
	extendedKeyUsage []x509.ExtKeyUsage,
	notBefore time.Time,
	notAfter time.Time,
) testCertificate {
	t.Helper()
	publicKey, privateKey, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	serialLimit := new(big.Int).Lsh(big.NewInt(1), 120)
	serial, err := rand.Int(rand.Reader, serialLimit)
	if err != nil {
		t.Fatal(err)
	}
	template := &x509.Certificate{
		SerialNumber: serial,
		Subject:      pkix.Name{CommonName: commonName},
		DNSNames:     dnsNames,
		IPAddresses:  ipAddresses,
		NotBefore:    notBefore,
		NotAfter:     notAfter,
		KeyUsage:     x509.KeyUsageDigitalSignature,
		ExtKeyUsage:  extendedKeyUsage,
	}
	der, err := x509.CreateCertificate(rand.Reader, template, authority.certificate, publicKey, authority.privateKey)
	if err != nil {
		t.Fatal(err)
	}
	return parseTestCertificate(t, der, privateKey)
}

func newSelfSignedServerCertificate(t *testing.T, notBefore, notAfter time.Time) testCertificate {
	t.Helper()
	publicKey, privateKey, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	template := &x509.Certificate{
		SerialNumber: big.NewInt(1),
		Subject:      pkix.Name{CommonName: "self-signed.test"},
		DNSNames:     []string{"localhost"},
		IPAddresses:  []net.IP{net.ParseIP("127.0.0.1")},
		NotBefore:    notBefore,
		NotAfter:     notAfter,
		KeyUsage:     x509.KeyUsageDigitalSignature,
		ExtKeyUsage:  []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
	}
	der, err := x509.CreateCertificate(rand.Reader, template, template, publicKey, privateKey)
	if err != nil {
		t.Fatal(err)
	}
	return parseTestCertificate(t, der, privateKey)
}

func parseTestCertificate(t *testing.T, der []byte, privateKey ed25519.PrivateKey) testCertificate {
	t.Helper()
	keyDER, err := x509.MarshalPKCS8PrivateKey(privateKey)
	if err != nil {
		t.Fatal(err)
	}
	certificatePEM := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})
	privateKeyPEM := pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: keyDER})
	tlsCertificate, err := tls.X509KeyPair(certificatePEM, privateKeyPEM)
	if err != nil {
		t.Fatal(err)
	}
	leaf, err := x509.ParseCertificate(der)
	if err != nil {
		t.Fatal(err)
	}
	tlsCertificate.Leaf = leaf
	return testCertificate{
		tlsCertificate: tlsCertificate,
		certificatePEM: certificatePEM,
		privateKeyPEM:  privateKeyPEM,
		leaf:           leaf,
	}
}

func startTestTLSServer(t *testing.T, certificate tls.Certificate, extra *tls.Config, handler http.Handler) *httptest.Server {
	t.Helper()
	config := &tls.Config{
		Certificates: []tls.Certificate{certificate},
		MinVersion:   tls.VersionTLS12,
	}
	if extra != nil {
		config.ClientAuth = extra.ClientAuth
		config.ClientCAs = extra.ClientCAs
		config.MaxVersion = extra.MaxVersion
	}
	server := httptest.NewUnstartedServer(handler)
	server.TLS = config
	server.StartTLS()
	return server
}

func writePEMFile(t *testing.T, name string, contents []byte) string {
	t.Helper()
	path := t.TempDir() + "/" + name
	if err := os.WriteFile(path, contents, 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

func mustURLHostname(t *testing.T, rawURL string) string {
	t.Helper()
	parsed, err := url.Parse(rawURL)
	if err != nil {
		t.Fatal(err)
	}
	return parsed.Hostname()
}

func replaceURLHostname(t *testing.T, rawURL, hostname string) string {
	t.Helper()
	parsed, err := url.Parse(rawURL)
	if err != nil {
		t.Fatal(err)
	}
	port := parsed.Port()
	parsed.Host = net.JoinHostPort(hostname, port)
	return parsed.String()
}
