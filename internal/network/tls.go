package network

import (
	"crypto/sha256"
	"crypto/tls"
	"crypto/x509"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
)

const maxCertificateFileBytes = 16 << 20

type tlsMaterial struct {
	rootCAs           *x509.CertPool
	clientCertificate *tls.Certificate
	trustedLeafSHA256 [sha256.Size]byte
	hasTrustedLeaf    bool
}

func loadTLSMaterial(settings Settings) (tlsMaterial, error) {
	var material tlsMaterial

	if settings.CAFile != "" {
		pemBytes, err := readBoundedFile(settings.CAFile)
		if err != nil {
			return tlsMaterial{}, fmt.Errorf("read CA file: %w", err)
		}
		roots, err := x509.SystemCertPool()
		if err != nil {
			return tlsMaterial{}, fmt.Errorf("load system certificate pool: %w", err)
		}
		if roots == nil {
			return tlsMaterial{}, errors.New("load system certificate pool: no certificate pool was returned")
		}
		if !roots.AppendCertsFromPEM(pemBytes) {
			return tlsMaterial{}, errors.New("CA file contains no valid PEM certificates")
		}
		material.rootCAs = roots
	}

	if settings.ClientCertificateFile != "" {
		certificatePEM, err := readBoundedFile(settings.ClientCertificateFile)
		if err != nil {
			return tlsMaterial{}, fmt.Errorf("read client certificate file: %w", err)
		}
		keyPEM, err := readBoundedFile(settings.ClientKeyFile)
		if err != nil {
			return tlsMaterial{}, fmt.Errorf("read client key file: %w", err)
		}
		certificate, err := tls.X509KeyPair(certificatePEM, keyPEM)
		if err != nil {
			return tlsMaterial{}, fmt.Errorf("load client certificate and key: %w", err)
		}
		material.clientCertificate = &certificate
	}

	if settings.TrustedCertificateSHA256 != "" {
		decoded, err := hex.DecodeString(settings.TrustedCertificateSHA256)
		if err != nil || len(decoded) != sha256.Size {
			return tlsMaterial{}, errors.New("trusted certificate SHA-256 is invalid")
		}
		copy(material.trustedLeafSHA256[:], decoded)
		material.hasTrustedLeaf = true
	}
	return material, nil
}

func readBoundedFile(path string) ([]byte, error) {
	file, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer file.Close()

	reader := io.LimitReader(file, maxCertificateFileBytes+1)
	data, err := io.ReadAll(reader)
	if err != nil {
		return nil, err
	}
	if len(data) > maxCertificateFileBytes {
		return nil, fmt.Errorf("file exceeds %d bytes", maxCertificateFileBytes)
	}
	return data, nil
}
