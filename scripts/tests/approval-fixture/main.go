// Command approval-fixture is a deterministic separate-process signer used only by
// the disposable LangChain interoperability proof. It is not a Reconc command
// or release artifact.
package main

import (
	"bytes"
	"crypto/ed25519"
	"crypto/sha256"
	"encoding/base64"
	"flag"
	"fmt"
	"io"
	"os"
	"time"

	"reconc.dev/reconc/internal/actionapproval"
)

const fixtureAuthorityID = "integration-authority"

func fixturePrivateKey() ed25519.PrivateKey {
	return ed25519.NewKeyFromSeed(bytes.Repeat([]byte{0x42}, ed25519.SeedSize))
}

func main() {
	if err := run(os.Args[1:], os.Stdin, os.Stdout); err != nil {
		_, _ = fmt.Fprintln(os.Stderr, "approval-fixture:", err)
		os.Exit(1)
	}
}

func run(args []string, input io.Reader, output io.Writer) error {
	set := flag.NewFlagSet("approval-fixture", flag.ContinueOnError)
	set.SetOutput(io.Discard)
	publicKey := set.Bool("public-key", false, "print the synthetic fixture public key")
	if err := set.Parse(args); err != nil {
		return err
	}
	if set.NArg() != 0 {
		return fmt.Errorf("positional arguments are not accepted")
	}
	privateKey := fixturePrivateKey()
	if *publicKey {
		encoded := base64.RawURLEncoding.EncodeToString(privateKey.Public().(ed25519.PublicKey))
		_, err := fmt.Fprintln(output, encoded)
		return err
	}
	body, err := io.ReadAll(io.LimitReader(input, actionapproval.MaxApprovalObjectBytes+1))
	if err != nil {
		return fmt.Errorf("read approval request: %w", err)
	}
	if len(body) == 0 || len(body) > actionapproval.MaxApprovalObjectBytes {
		return fmt.Errorf("approval request is outside its byte boundary")
	}
	request, err := actionapproval.DecodeRequest(body)
	if err != nil {
		return err
	}
	signedAt, err := time.Parse(time.RFC3339Nano, request.IssuedAt)
	if err != nil {
		return fmt.Errorf("parse approval issuance time: %w", err)
	}
	entropy := sha256.Sum256([]byte(request.RequestID))
	_, receipt, err := actionapproval.SignReceipt(
		request,
		fixtureAuthorityID,
		privateKey,
		actionapproval.DecisionApprove,
		signedAt,
		bytes.NewReader(entropy[:]),
	)
	if err != nil {
		return err
	}
	if _, err := output.Write(receipt); err != nil {
		return fmt.Errorf("write approval receipt: %w", err)
	}
	return nil
}
