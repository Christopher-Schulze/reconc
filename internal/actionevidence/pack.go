package actionevidence

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/url"
	"sort"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"reconc.dev/reconc/internal/action"
	"reconc.dev/reconc/internal/actioninspect"
	"reconc.dev/reconc/internal/boundedio"
)

const packSigningContext = "reconc.action-control-map-signature/v1\x00"

func LoadPack(path string, authentication PackAuthentication) (LoadedPack, error) {
	body, err := boundedio.ReadRegularFile(path, MaxPackBytes)
	if err != nil {
		return LoadedPack{}, fmt.Errorf("read bounded control-map pack: %w", err)
	}
	pack, err := DecodePack(body)
	if err != nil {
		return LoadedPack{}, err
	}
	identity, err := PackIdentity(pack)
	if err != nil {
		return LoadedPack{}, err
	}
	provenance, err := authenticatePack(identity, authentication)
	if err != nil {
		return LoadedPack{}, err
	}
	return LoadedPack{Pack: pack, Identity: identity, Provenance: provenance}, nil
}

func DecodePack(body []byte) (Pack, error) {
	if len(body) == 0 || len(body) > MaxPackBytes || !utf8.Valid(body) {
		return Pack{}, fmt.Errorf("control-map pack must contain 1 to %d valid UTF-8 bytes", MaxPackBytes)
	}
	var pack Pack
	if err := decodeStrictObject(body, &pack); err != nil {
		return Pack{}, fmt.Errorf("decode control-map pack: %w", err)
	}
	if err := ValidatePack(pack); err != nil {
		return Pack{}, err
	}
	return pack, nil
}

func ValidatePack(pack Pack) error {
	if pack.Schema != PackSchema || pack.FormatVersion != FormatVersion ||
		!action.SafeLabel(pack.PackID) || !action.SafeLabel(pack.PackVersion) ||
		pack.ReviewStatus != "reviewed" && pack.ReviewStatus != "stale" && pack.ReviewStatus != "not-reviewed" ||
		pack.Controls == nil || len(pack.Controls) == 0 || len(pack.Controls) > MaxControls {
		return fmt.Errorf("control-map pack metadata or bounds are invalid")
	}
	if err := validateSource(pack.Source); err != nil {
		return err
	}
	scanner, err := actioninspect.NewTextScanner()
	if err != nil {
		return fmt.Errorf("prepare control-map privacy scanner: %w", err)
	}
	if err := validateTextFields(scanner, pack.Framework, pack.Source.URL, pack.Source.Edition, pack.Source.ReuseNotice); err != nil {
		return err
	}
	for index, control := range pack.Controls {
		if index > 0 && pack.Controls[index-1].ID >= control.ID {
			return fmt.Errorf("control-map controls must have unique lexically sorted IDs")
		}
		if err := validateControl(scanner, control); err != nil {
			return fmt.Errorf("control %q: %w", control.ID, err)
		}
	}
	return nil
}

func validateSource(source Source) error {
	parsed, err := url.Parse(source.URL)
	if err != nil || parsed.Scheme != "https" || parsed.Host == "" || parsed.User != nil || parsed.Fragment != "" {
		return fmt.Errorf("control-map source must be an absolute HTTPS URL without credentials or fragment")
	}
	if !canonicalDate(source.SourceDate) || !canonicalDate(source.ReviewedAt) {
		return fmt.Errorf("control-map source dates must be canonical YYYY-MM-DD values")
	}
	return nil
}

func validateControl(scanner *actioninspect.TextScanner, control Control) error {
	if !validControlID(control.ID) || control.EvidenceSelectors == nil ||
		len(control.EvidenceSelectors) == 0 || len(control.EvidenceSelectors) > MaxSelectorsPerControl ||
		control.KnownGaps == nil || len(control.KnownGaps) > MaxGapsPerControl {
		return fmt.Errorf("metadata or bounds are invalid")
	}
	if err := validateTextFields(scanner, control.Reference, control.Rationale); err != nil {
		return err
	}
	if err := validateCanonicalSelectors(control.EvidenceSelectors); err != nil {
		return err
	}
	if !sort.StringsAreSorted(control.KnownGaps) {
		return fmt.Errorf("known gaps must be lexically sorted")
	}
	for index, gap := range control.KnownGaps {
		if index > 0 && control.KnownGaps[index-1] == gap {
			return fmt.Errorf("known gaps must be unique")
		}
		if err := validateTextFields(scanner, gap); err != nil {
			return err
		}
	}
	return nil
}

func validateCanonicalSelectors(selectors []FactID) error {
	known := make(map[FactID]bool, len(AllFactIDs()))
	for _, id := range AllFactIDs() {
		known[id] = true
	}
	for index, selector := range selectors {
		if !known[selector] || index > 0 && selectors[index-1] >= selector {
			return fmt.Errorf("evidence selectors must be known, unique, and lexically sorted")
		}
	}
	return nil
}

func validateTextFields(scanner *actioninspect.TextScanner, values ...string) error {
	if scanner == nil {
		return fmt.Errorf("control-map privacy scanner is unavailable")
	}
	for _, value := range values {
		if err := validateTextField(scanner, value); err != nil {
			return err
		}
	}
	return nil
}

func validateTextField(scanner *actioninspect.TextScanner, value string) error {
	if value == "" || len(value) > MaxTextBytes || !utf8.ValidString(value) || strings.TrimSpace(value) != value {
		return fmt.Errorf("text must contain 1 to %d trimmed UTF-8 bytes", MaxTextBytes)
	}
	for _, character := range value {
		if character == '\n' || character == '\r' || character == 0 || unicode.IsControl(character) {
			return fmt.Errorf("text must be one printable line")
		}
	}
	if containsForbiddenClaim(value) {
		return fmt.Errorf("text contains a forbidden assurance claim")
	}
	categories, err := scanner.PrivateCategories(context.Background(), value, uint64(len(value)))
	if err != nil {
		return fmt.Errorf("scan control-map text: %w", err)
	}
	if len(categories) != 0 {
		return fmt.Errorf("text contains private-data-shaped material")
	}
	return nil
}

func containsForbiddenClaim(value string) bool {
	lower := strings.ToLower(value)
	for _, forbidden := range []string{
		"certified", "compliant", "guaranteed", "regulator-approved",
		"regulator approved", "approved by a regulator", "legally sufficient",
	} {
		if strings.Contains(lower, forbidden) {
			return true
		}
	}
	return false
}

func PackIdentity(pack Pack) (string, error) {
	if err := ValidatePack(pack); err != nil {
		return "", err
	}
	body, err := json.Marshal(pack)
	if err != nil {
		return "", fmt.Errorf("encode control-map identity: %w", err)
	}
	digest := sha256.Sum256(body)
	return "sha256:" + hex.EncodeToString(digest[:]), nil
}

func authenticatePack(identity string, authentication PackAuthentication) (string, error) {
	digestPinned := authentication.ExpectedDigest != ""
	signed := authentication.SignaturePath != "" || authentication.RegistryPath != ""
	if digestPinned == signed {
		return "", fmt.Errorf("control-map pack requires exactly one digest or signature authentication")
	}
	if digestPinned {
		if !action.ValidSHA256Identity(authentication.ExpectedDigest) ||
			subtle.ConstantTimeCompare([]byte(identity), []byte(authentication.ExpectedDigest)) != 1 {
			return "", fmt.Errorf("control-map pack digest does not match")
		}
		return "digest-pinned", nil
	}
	if authentication.SignaturePath == "" || authentication.RegistryPath == "" {
		return "", fmt.Errorf("signed control-map pack requires signature and authority registry paths")
	}
	return verifyPackSignature(identity, authentication.SignaturePath, authentication.RegistryPath)
}

func verifyPackSignature(identity, signaturePath, registryPath string) (string, error) {
	signatureBody, err := boundedio.ReadRegularFile(signaturePath, 64<<10)
	if err != nil {
		return "", fmt.Errorf("read control-map signature: %w", err)
	}
	registryBody, err := boundedio.ReadRegularFile(registryPath, MaxAuthorityRegistrySize)
	if err != nil {
		return "", fmt.Errorf("read control-map authority registry: %w", err)
	}
	var signature PackSignature
	if err := decodeStrictObject(signatureBody, &signature); err != nil {
		return "", fmt.Errorf("decode control-map signature: %w", err)
	}
	registry, err := decodeMappingAuthorityRegistry(registryBody)
	if err != nil {
		return "", err
	}
	key, err := validatePackSignature(signature, identity, registry)
	if err != nil {
		return "", err
	}
	return "signed:" + key, nil
}

func validatePackSignature(signature PackSignature, identity string, registry map[string]ed25519.PublicKey) (string, error) {
	if signature.Schema != PackSignatureSchema || signature.FormatVersion != FormatVersion ||
		signature.PackIdentity != identity || !action.SafeLabel(signature.AuthorityKeyID) {
		return "", fmt.Errorf("control-map signature metadata is invalid")
	}
	encoded, err := base64.RawURLEncoding.Strict().DecodeString(signature.Signature)
	if err != nil || len(encoded) != ed25519.SignatureSize ||
		base64.RawURLEncoding.EncodeToString(encoded) != signature.Signature {
		return "", fmt.Errorf("control-map signature encoding is invalid")
	}
	key, exists := registry[signature.AuthorityKeyID]
	if !exists || !ed25519.Verify(key, []byte(packSigningContext+identity), encoded) {
		return "", fmt.Errorf("control-map signature verification failed")
	}
	return signature.AuthorityKeyID, nil
}

func decodeMappingAuthorityRegistry(body []byte) (map[string]ed25519.PublicKey, error) {
	var registry MappingAuthorityRegistry
	if err := decodeStrictObject(body, &registry); err != nil {
		return nil, fmt.Errorf("decode control-map authority registry: %w", err)
	}
	if registry.Schema != AuthorityRegistrySchema || registry.FormatVersion != FormatVersion ||
		registry.Authorities == nil || len(registry.Authorities) == 0 ||
		len(registry.Authorities) > MaxMappingAuthorities {
		return nil, fmt.Errorf("control-map authority registry metadata or bounds are invalid")
	}
	return compileMappingAuthorities(registry.Authorities)
}

func compileMappingAuthorities(authorities []MappingAuthority) (map[string]ed25519.PublicKey, error) {
	out := make(map[string]ed25519.PublicKey, len(authorities))
	publicKeys := make(map[string]string, len(authorities))
	for index, authority := range authorities {
		if !action.SafeLabel(authority.ID) || index > 0 && authorities[index-1].ID >= authority.ID {
			return nil, fmt.Errorf("control-map authorities must have unique lexically sorted IDs")
		}
		key, err := base64.RawURLEncoding.Strict().DecodeString(authority.PublicKey)
		if err != nil || len(key) != ed25519.PublicKeySize ||
			base64.RawURLEncoding.EncodeToString(key) != authority.PublicKey {
			return nil, fmt.Errorf("control-map authority %q key is invalid", authority.ID)
		}
		if previous, duplicate := publicKeys[authority.PublicKey]; duplicate {
			return nil, fmt.Errorf("control-map authorities %q and %q alias one public key", previous, authority.ID)
		}
		publicKeys[authority.PublicKey] = authority.ID
		out[authority.ID] = append(ed25519.PublicKey(nil), key...)
	}
	return out, nil
}

func decodeStrictObject(body []byte, target any) error {
	if _, err := action.ParseObjectJSON(body); err != nil {
		return err
	}
	decoder := json.NewDecoder(bytes.NewReader(body))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(target); err != nil {
		return err
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		return fmt.Errorf("expected exactly one JSON object")
	}
	return nil
}

func canonicalDate(value string) bool {
	parsed, err := time.Parse("2006-01-02", value)
	return err == nil && parsed.Format("2006-01-02") == value
}

func validControlID(value string) bool {
	if len(value) == 0 || len(value) > 128 {
		return false
	}
	for index, character := range value {
		if character >= 'a' && character <= 'z' || character >= '0' && character <= '9' ||
			index > 0 && strings.ContainsRune(".-", character) {
			continue
		}
		return false
	}
	return true
}
