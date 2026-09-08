// Command sign-ci-evidence runs only in an operator-controlled CI issuer.
// Its caller must authenticate provider results before supplying the statement;
// the signing key and this program must be outside candidate write authority.
package main

import (
	"bytes"
	"crypto/ed25519"
	"crypto/x509"
	"encoding/pem"
	"flag"
	"fmt"
	"io"
	"os"

	"reconc.dev/reconc/internal/boundedio"
	"reconc.dev/reconc/internal/cievidence"
)

func main() {
	if err := run(os.Args[1:], os.Stdout); err != nil {
		fmt.Fprintln(os.Stderr, "sign-ci-evidence:", err)
		os.Exit(1)
	}
}

func run(args []string, output io.Writer) error {
	flags := flag.NewFlagSet("sign-ci-evidence", flag.ContinueOnError)
	flags.SetOutput(io.Discard)
	var statementPath, keyPath, authority string
	flags.StringVar(&statementPath, "statement", "", "authenticated provider statement JSON")
	flags.StringVar(&keyPath, "key", "", "operator-owned Ed25519 PKCS#8 PEM private key")
	flags.StringVar(&authority, "authority", "", "trusted authority key ID")
	if err := flags.Parse(args); err != nil {
		return err
	}
	if flags.NArg() != 0 || statementPath == "" || keyPath == "" || authority == "" {
		return fmt.Errorf("usage: sign-ci-evidence --statement FILE --key FILE --authority ID")
	}
	body, err := signFile(statementPath, keyPath, authority)
	if err != nil {
		return err
	}
	written, err := output.Write(body)
	if err != nil {
		return fmt.Errorf("write signed CI evidence: %w", err)
	}
	if written != len(body) {
		return io.ErrShortWrite
	}
	return nil
}

func signFile(statementPath, keyPath, authority string) ([]byte, error) {
	body, err := boundedio.ReadRegularFile(statementPath, cievidence.MaxBytes)
	if err != nil {
		return nil, fmt.Errorf("read provider statement: %w", err)
	}
	statement, err := cievidence.DecodeStatement(body)
	if err != nil {
		return nil, err
	}
	keyBody, err := boundedio.ReadRegularFile(keyPath, 4<<10)
	if err != nil {
		return nil, fmt.Errorf("read CI signing key: %w", err)
	}
	defer clear(keyBody)
	key, err := decodeKey(keyBody)
	if err != nil {
		return nil, err
	}
	defer clear(key)
	return cievidence.Sign(statement, authority, key)
}

func decodeKey(body []byte) (ed25519.PrivateKey, error) {
	if !bytes.HasPrefix(bytes.TrimSpace(body), []byte("-----BEGIN "+"PRIVATE KEY-----")) {
		return nil, fmt.Errorf("CI signing key must be a PKCS#8 private-key PEM block")
	}
	block, rest := pem.Decode(body)
	if block == nil {
		return nil, fmt.Errorf("CI signing key PEM is malformed")
	}
	defer clear(block.Bytes)
	if block.Type != "PRIVATE KEY" || len(block.Headers) != 0 || len(bytes.TrimSpace(rest)) != 0 {
		return nil, fmt.Errorf("CI signing key must contain exactly one unencrypted private-key block")
	}
	parsed, err := x509.ParsePKCS8PrivateKey(block.Bytes)
	if err != nil {
		return nil, fmt.Errorf("CI signing key PKCS#8 is invalid")
	}
	key, ok := parsed.(ed25519.PrivateKey)
	if !ok || len(key) != ed25519.PrivateKeySize {
		return nil, fmt.Errorf("CI signing key must use Ed25519")
	}
	return key, nil
}
