// Package pki wraps Easy-RSA/OpenVPN and independently verifies their output.
package pki

import (
	"bytes"
	"crypto"
	"crypto/sha256"
	"crypto/x509"
	"encoding/hex"
	"encoding/pem"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"
	"time"

	"github.com/yjrszcq/openvpn-docker/internal/domain"
)

const maxCryptoFileSize = 4 << 20

var ErrInvalidMaterial = errors.New("PKI material is invalid")

type CertificateInfo struct {
	Serial      string
	Fingerprint [32]byte
	NotBefore   time.Time
	NotAfter    time.Time
}

type Authority struct {
	CA     CertificateInfo
	Server CertificateInfo
}

func ValidateAuthority(pkiDir, serverName string, now time.Time) (Authority, error) {
	ca, err := readCertificate(pkiDir + "/ca.crt")
	if err != nil {
		return Authority{}, err
	}
	caInfo, err := validateCA(ca, now)
	if err != nil {
		return Authority{}, err
	}
	caKey, err := readPrivateKey(pkiDir + "/private/ca.key")
	if err != nil {
		return Authority{}, err
	}
	if err := verifyKeyPair(ca, caKey); err != nil {
		return Authority{}, fmt.Errorf("CA certificate/key: %w", err)
	}
	server, err := readCertificate(pkiDir + "/issued/" + serverName + ".crt")
	if err != nil {
		return Authority{}, err
	}
	serverKey, err := readPrivateKey(pkiDir + "/private/" + serverName + ".key")
	if err != nil {
		return Authority{}, err
	}
	if server.Subject.CommonName != serverName {
		return Authority{}, fmt.Errorf("%w: server certificate common name mismatch", ErrInvalidMaterial)
	}
	if err := verifyCertificate(server, ca, x509.ExtKeyUsageServerAuth, now); err != nil {
		return Authority{}, fmt.Errorf("server certificate: %w", err)
	}
	if err := verifyKeyPair(server, serverKey); err != nil {
		return Authority{}, fmt.Errorf("server certificate/key: %w", err)
	}
	if err := ValidateCRL(pkiDir+"/crl.pem", ca, now); err != nil {
		return Authority{}, err
	}
	return Authority{CA: caInfo, Server: certificateInfo(server)}, nil
}

// ValidateCA verifies the persisted trust anchor and returns its stable identity.
func ValidateCA(pkiDir string, now time.Time) (CertificateInfo, error) {
	ca, err := readCertificate(pkiDir + "/ca.crt")
	if err != nil {
		return CertificateInfo{}, err
	}
	return validateCA(ca, now)
}

// ValidateCACertificate validates in-memory public recovery evidence.
func ValidateCACertificate(data []byte, now time.Time) (CertificateInfo, error) {
	certificate, err := decodeCertificate(data)
	if err != nil {
		return CertificateInfo{}, err
	}
	return validateCA(certificate, now)
}

// ValidateClientMaterial validates in-memory profile evidence without writing
// private material to a temporary file.
func ValidateClientMaterial(caData, certificateData, keyData []byte, clientID string, now time.Time) (CertificateInfo, error) {
	if !domain.ValidUUID(clientID) {
		return CertificateInfo{}, fmt.Errorf("invalid client UUID")
	}
	ca, err := decodeCertificate(caData)
	if err != nil {
		return CertificateInfo{}, err
	}
	if _, err := validateCA(ca, now); err != nil {
		return CertificateInfo{}, err
	}
	certificate, err := decodeCertificate(certificateData)
	if err != nil {
		return CertificateInfo{}, err
	}
	key, err := decodePrivateKey(keyData)
	if err != nil {
		return CertificateInfo{}, err
	}
	if certificate.Subject.CommonName != clientID {
		return CertificateInfo{}, fmt.Errorf("%w: client certificate common name mismatch", ErrInvalidMaterial)
	}
	if err := verifyCertificate(certificate, ca, x509.ExtKeyUsageClientAuth, now); err != nil {
		return CertificateInfo{}, fmt.Errorf("client certificate: %w", err)
	}
	if err := verifyKeyPair(certificate, key); err != nil {
		return CertificateInfo{}, fmt.Errorf("client certificate/key: %w", err)
	}
	return certificateInfo(certificate), nil
}

// ValidateCAKeyPair verifies that the signing private key belongs to the
// persisted trust anchor. It is kept separate from ValidateCA so diagnostics
// can distinguish a recoverable public certificate from a lost signing key.
func ValidateCAKeyPair(pkiDir string, now time.Time) (CertificateInfo, error) {
	ca, err := readCertificate(pkiDir + "/ca.crt")
	if err != nil {
		return CertificateInfo{}, err
	}
	info, err := validateCA(ca, now)
	if err != nil {
		return CertificateInfo{}, err
	}
	key, err := readPrivateKey(pkiDir + "/private/ca.key")
	if err != nil {
		return CertificateInfo{}, err
	}
	if err := verifyKeyPair(ca, key); err != nil {
		return CertificateInfo{}, fmt.Errorf("CA certificate/key: %w", err)
	}
	return info, nil
}

// ValidateServer verifies the server identity without requiring a valid CRL.
func ValidateServer(pkiDir, serverName string, now time.Time) (CertificateInfo, error) {
	ca, err := readCertificate(pkiDir + "/ca.crt")
	if err != nil {
		return CertificateInfo{}, err
	}
	if _, err := validateCA(ca, now); err != nil {
		return CertificateInfo{}, err
	}
	certificate, err := readCertificate(pkiDir + "/issued/" + serverName + ".crt")
	if err != nil {
		return CertificateInfo{}, err
	}
	key, err := readPrivateKey(pkiDir + "/private/" + serverName + ".key")
	if err != nil {
		return CertificateInfo{}, err
	}
	if certificate.Subject.CommonName != serverName {
		return CertificateInfo{}, fmt.Errorf("%w: server certificate common name mismatch", ErrInvalidMaterial)
	}
	if err := verifyCertificate(certificate, ca, x509.ExtKeyUsageServerAuth, now); err != nil {
		return CertificateInfo{}, fmt.Errorf("server certificate: %w", err)
	}
	if err := verifyKeyPair(certificate, key); err != nil {
		return CertificateInfo{}, fmt.Errorf("server certificate/key: %w", err)
	}
	return certificateInfo(certificate), nil
}

// ValidateCRLForCA validates the current CRL against the persisted CA.
func ValidateCRLForCA(pkiDir string, now time.Time) error {
	ca, err := readCertificate(pkiDir + "/ca.crt")
	if err != nil {
		return err
	}
	if _, err := validateCA(ca, now); err != nil {
		return err
	}
	return ValidateCRL(pkiDir+"/crl.pem", ca, now)
}

// ValidateRevocationStatus verifies that a certificate serial is present in
// the current CRL exactly when SQLite marks the client revoked.
func ValidateRevocationStatus(pkiDir, serial string, revoked bool, now time.Time) error {
	ca, err := readCertificate(pkiDir + "/ca.crt")
	if err != nil {
		return err
	}
	if _, err := validateCA(ca, now); err != nil {
		return err
	}
	data, err := readRegularFile(pkiDir+"/crl.pem", 0o644)
	if err != nil {
		return err
	}
	block, _ := pem.Decode(data)
	if block == nil || block.Type != "X509 CRL" {
		return fmt.Errorf("%w: CRL PEM is invalid", ErrInvalidMaterial)
	}
	list, err := x509.ParseRevocationList(block.Bytes)
	if err != nil {
		return fmt.Errorf("%w: parse CRL: %v", ErrInvalidMaterial, err)
	}
	if err := ValidateCRL(pkiDir+"/crl.pem", ca, now); err != nil {
		return err
	}
	found := false
	for _, entry := range list.RevokedCertificateEntries {
		if entry.SerialNumber != nil && strings.EqualFold(entry.SerialNumber.Text(16), serial) {
			found = true
			break
		}
	}
	if found != revoked {
		return fmt.Errorf("%w: certificate revocation status does not match SQLite", ErrInvalidMaterial)
	}
	return nil
}

func ValidateClient(pkiDir, clientID string, now time.Time) (CertificateInfo, error) {
	ca, err := readCertificate(pkiDir + "/ca.crt")
	if err != nil {
		return CertificateInfo{}, err
	}
	if _, err := validateCA(ca, now); err != nil {
		return CertificateInfo{}, err
	}
	certificate, err := readCertificate(pkiDir + "/issued/" + clientID + ".crt")
	if err != nil {
		return CertificateInfo{}, err
	}
	key, err := readPrivateKey(pkiDir + "/private/" + clientID + ".key")
	if err != nil {
		return CertificateInfo{}, err
	}
	if certificate.Subject.CommonName != clientID {
		return CertificateInfo{}, fmt.Errorf("%w: client certificate common name mismatch", ErrInvalidMaterial)
	}
	if err := verifyCertificate(certificate, ca, x509.ExtKeyUsageClientAuth, now); err != nil {
		return CertificateInfo{}, fmt.Errorf("client certificate: %w", err)
	}
	if err := verifyKeyPair(certificate, key); err != nil {
		return CertificateInfo{}, fmt.Errorf("client certificate/key: %w", err)
	}
	return certificateInfo(certificate), nil
}

func validateCA(ca *x509.Certificate, now time.Time) (CertificateInfo, error) {
	if !ca.IsCA || ca.KeyUsage&x509.KeyUsageCertSign == 0 {
		return CertificateInfo{}, fmt.Errorf("%w: CA certificate cannot sign certificates", ErrInvalidMaterial)
	}
	if ca.Subject.CommonName != "OpenVPN Container CA" {
		return CertificateInfo{}, fmt.Errorf("%w: CA certificate common name mismatch", ErrInvalidMaterial)
	}
	if err := ca.CheckSignatureFrom(ca); err != nil {
		return CertificateInfo{}, fmt.Errorf("%w: CA certificate is not self-signed: %v", ErrInvalidMaterial, err)
	}
	if err := validAt(ca, now); err != nil {
		return CertificateInfo{}, fmt.Errorf("CA certificate: %w", err)
	}
	return certificateInfo(ca), nil
}

func ValidateCRL(filePath string, ca *x509.Certificate, now time.Time) error {
	data, err := readRegularFile(filePath, 0o644)
	if err != nil {
		return err
	}
	block, _ := pem.Decode(data)
	if block == nil || block.Type != "X509 CRL" {
		return fmt.Errorf("%w: CRL PEM is invalid", ErrInvalidMaterial)
	}
	list, err := x509.ParseRevocationList(block.Bytes)
	if err != nil {
		return fmt.Errorf("%w: parse CRL: %v", ErrInvalidMaterial, err)
	}
	if err := list.CheckSignatureFrom(ca); err != nil {
		return fmt.Errorf("%w: CRL signature does not match CA: %v", ErrInvalidMaterial, err)
	}
	if !list.ThisUpdate.IsZero() && now.Before(list.ThisUpdate) {
		return fmt.Errorf("%w: CRL is not active yet", ErrInvalidMaterial)
	}
	if list.NextUpdate.IsZero() || !now.Before(list.NextUpdate) {
		return fmt.Errorf("%w: CRL is expired", ErrInvalidMaterial)
	}
	return nil
}

func ValidateTLSCryptKey(filePath string) error {
	data, err := readRegularFile(filePath, 0o600)
	if err != nil {
		return err
	}
	return ValidateTLSCryptData(data)
}

// ValidateTLSCryptData validates in-memory profile recovery evidence.
func ValidateTLSCryptData(data []byte) error {
	const begin = "-----BEGIN OpenVPN Static key V1-----"
	const end = "-----END OpenVPN Static key V1-----"
	inside := false
	ended := false
	var encoded strings.Builder
	for _, raw := range strings.Split(string(data), "\n") {
		line := strings.TrimSpace(raw)
		switch {
		case line == begin && !inside && !ended:
			inside = true
		case line == end && inside && !ended:
			inside = false
			ended = true
		case inside:
			encoded.WriteString(line)
		case line == "" || strings.HasPrefix(line, "#"):
		default:
			return fmt.Errorf("%w: tls-crypt key contains unexpected data", ErrInvalidMaterial)
		}
	}
	if inside || !ended || encoded.Len() != 512 {
		return fmt.Errorf("%w: tls-crypt key must contain exactly 256 bytes", ErrInvalidMaterial)
	}
	decoded, err := hex.DecodeString(encoded.String())
	if err != nil {
		return fmt.Errorf("%w: tls-crypt key is not hexadecimal", ErrInvalidMaterial)
	}
	if bytes.Equal(decoded, make([]byte, len(decoded))) {
		return fmt.Errorf("%w: tls-crypt key is all zero", ErrInvalidMaterial)
	}
	return nil
}

func readCertificate(filePath string) (*x509.Certificate, error) {
	data, err := readRegularFile(filePath, 0o644)
	if err != nil {
		return nil, err
	}
	return decodeCertificate(data)
}

func decodeCertificate(data []byte) (*x509.Certificate, error) {
	for len(data) > 0 {
		block, rest := pem.Decode(data)
		if block == nil {
			break
		}
		if block.Type == "CERTIFICATE" {
			certificate, err := x509.ParseCertificate(block.Bytes)
			if err != nil {
				return nil, fmt.Errorf("%w: parse certificate: %v", ErrInvalidMaterial, err)
			}
			return certificate, nil
		}
		data = rest
	}
	return nil, fmt.Errorf("%w: certificate PEM is missing", ErrInvalidMaterial)
}

func readPrivateKey(filePath string) (crypto.PrivateKey, error) {
	data, err := readRegularFile(filePath, 0o600)
	if err != nil {
		return nil, err
	}
	return decodePrivateKey(data)
}

func decodePrivateKey(data []byte) (crypto.PrivateKey, error) {
	var err error
	for len(data) > 0 {
		block, rest := pem.Decode(data)
		if block == nil {
			break
		}
		var key any
		switch block.Type {
		case "PRIVATE KEY":
			key, err = x509.ParsePKCS8PrivateKey(block.Bytes)
		case "RSA PRIVATE KEY":
			key, err = x509.ParsePKCS1PrivateKey(block.Bytes)
		case "EC PRIVATE KEY":
			key, err = x509.ParseECPrivateKey(block.Bytes)
		default:
			data = rest
			continue
		}
		if err != nil {
			return nil, fmt.Errorf("%w: parse private key: %v", ErrInvalidMaterial, err)
		}
		if _, ok := key.(crypto.Signer); !ok {
			return nil, fmt.Errorf("%w: private key cannot sign", ErrInvalidMaterial)
		}
		return key, nil
	}
	return nil, fmt.Errorf("%w: private key PEM is missing", ErrInvalidMaterial)
}

func readRegularFile(filePath string, expectedMode os.FileMode) ([]byte, error) {
	info, err := os.Lstat(filePath)
	if err != nil {
		return nil, fmt.Errorf("read PKI file %s: %w", filePath, err)
	}
	if !info.Mode().IsRegular() || info.Mode()&os.ModeSymlink != 0 {
		return nil, fmt.Errorf("%w: PKI path is not a regular file", ErrInvalidMaterial)
	}
	if info.Mode().Perm() != expectedMode {
		return nil, fmt.Errorf("%w: PKI file %s has mode %04o, want %04o", ErrInvalidMaterial, filePath, info.Mode().Perm(), expectedMode)
	}
	file, err := os.Open(filePath)
	if err != nil {
		return nil, err
	}
	defer file.Close()
	data, err := io.ReadAll(io.LimitReader(file, maxCryptoFileSize+1))
	if err != nil {
		return nil, err
	}
	if len(data) > maxCryptoFileSize {
		return nil, fmt.Errorf("%w: PKI file exceeds size limit", ErrInvalidMaterial)
	}
	return data, nil
}

func verifyCertificate(certificate, ca *x509.Certificate, usage x509.ExtKeyUsage, now time.Time) error {
	roots := x509.NewCertPool()
	roots.AddCert(ca)
	if _, err := certificate.Verify(x509.VerifyOptions{Roots: roots, CurrentTime: now, KeyUsages: []x509.ExtKeyUsage{usage}}); err != nil {
		return fmt.Errorf("%w: certificate verification failed: %v", ErrInvalidMaterial, err)
	}
	return nil
}

func verifyKeyPair(certificate *x509.Certificate, privateKey crypto.PrivateKey) error {
	signer, ok := privateKey.(crypto.Signer)
	if !ok {
		return fmt.Errorf("%w: private key cannot sign", ErrInvalidMaterial)
	}
	certificatePublic, err := x509.MarshalPKIXPublicKey(certificate.PublicKey)
	if err != nil {
		return fmt.Errorf("%w: encode certificate public key", ErrInvalidMaterial)
	}
	privatePublic, err := x509.MarshalPKIXPublicKey(signer.Public())
	if err != nil {
		return fmt.Errorf("%w: encode private public key", ErrInvalidMaterial)
	}
	if !bytes.Equal(certificatePublic, privatePublic) {
		return fmt.Errorf("%w: certificate and private key do not match", ErrInvalidMaterial)
	}
	return nil
}

func validAt(certificate *x509.Certificate, now time.Time) error {
	if now.Before(certificate.NotBefore) || !now.Before(certificate.NotAfter) {
		return fmt.Errorf("%w: certificate is outside its validity period", ErrInvalidMaterial)
	}
	return nil
}

func certificateInfo(certificate *x509.Certificate) CertificateInfo {
	serial := "0"
	if certificate.SerialNumber != nil {
		serial = strings.ToUpper(certificate.SerialNumber.Text(16))
	}
	return CertificateInfo{Serial: serial, Fingerprint: sha256.Sum256(certificate.Raw), NotBefore: certificate.NotBefore, NotAfter: certificate.NotAfter}
}
