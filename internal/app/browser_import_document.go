package app

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/url"
	"slices"
	"strconv"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/yzgolden86/PivotFlow/internal/model"
	"github.com/yzgolden86/PivotFlow/internal/site/provider"
)

const (
	browserImportMaxBytes   = 2 << 20
	browserImportMaxEntries = 64
	browserImportMaxSecret  = 16 << 10
	browserBridgeFormat     = "pivotflow-browser-bridge"
	allAPIHubFormat         = "all-api-hub"
)

type browserImportEntry struct {
	SourceOrigin   string               `json:"source_origin"`
	BaseURL        string               `json:"base_url"`
	SiteName       string               `json:"site_name"`
	Platform       string               `json:"platform"`
	AccountLabel   string               `json:"account_label"`
	CredentialType string               `json:"credential_type"`
	Credential     provider.Credentials `json:"credential"`
	RefreshPresent bool                 `json:"-"`
	ExpiryPresent  bool                 `json:"-"`
	Problem        string               `json:"-"`
	DuplicateOf    *int                 `json:"-"`
}

type browserImportDocument struct {
	Format          string
	CapturedAt      int64
	Entries         []browserImportEntry
	IgnoredSections []string
}

// Decode the original text as well as JSON API callers. The browser sends file
// text so duplicate keys cannot disappear in its JSON.parse before inspection.
func decodeBrowserImportDocument(raw json.RawMessage) (*browserImportDocument, error) {
	if len(raw) > browserImportMaxBytes {
		return nil, previewFailure("", "browser_import_too_large")
	}
	value, err := decodeBrowserImportJSON(raw)
	if err != nil {
		return nil, err
	}
	if text, ok := value.(string); ok {
		if len(text) > browserImportMaxBytes {
			return nil, previewFailure("", "browser_import_too_large")
		}
		value, err = decodeBrowserImportJSON([]byte(text))
		if err != nil {
			return nil, err
		}
	}
	root, ok := value.(map[string]any)
	if !ok {
		return nil, previewFailure("", "browser_import_invalid_document")
	}
	document := &browserImportDocument{IgnoredSections: []string{}}
	var entries []any
	if stringField(root, "format") == browserBridgeFormat {
		if stringField(root, "version") != "1.0" {
			return nil, previewFailure("", "browser_import_unsupported_version")
		}
		document.Format = browserBridgeFormat
		document.CapturedAt, _ = browserImportInt(root["captured_at"])
		entries, ok = root["entries"].([]any)
		if !ok {
			return nil, previewFailure("", "browser_import_invalid_document")
		}
	} else {
		if root["format"] != nil || !slices.Contains([]string{"2.0", "3.0", "4.0"}, stringField(root, "version")) {
			return nil, previewFailure("", "browser_import_unsupported_version")
		}
		document.Format = allAPIHubFormat
		document.CapturedAt, _ = browserImportInt(root["timestamp"])
		accounts := root["accounts"]
		if config, isConfig := accounts.(map[string]any); isConfig {
			accounts = config["accounts"]
		}
		entries, ok = accounts.([]any)
		if !ok {
			return nil, previewFailure("", "browser_import_invalid_document")
		}
		for _, section := range []string{"apiCredentialProfiles", "channelConfigs", "featureGuidance", "preferences", "tagStore"} {
			if _, exists := root[section]; exists {
				document.IgnoredSections = append(document.IgnoredSections, section)
			}
		}
	}
	if len(entries) == 0 || len(entries) > browserImportMaxEntries {
		return nil, previewFailure("", "browser_import_entry_limit")
	}
	for _, value := range entries {
		entry := decodeBrowserImportEntry(value, document.Format)
		normalizeBrowserImportEntry(&entry)
		document.Entries = append(document.Entries, entry)
	}
	markBrowserImportDuplicates(document.Entries)
	return document, nil
}

func decodeBrowserImportEntry(value any, format string) browserImportEntry {
	entry := browserImportEntry{}
	row, ok := value.(map[string]any)
	if !ok {
		entry.Problem = "invalid_entry"
		return entry
	}
	if format == browserBridgeFormat {
		allowed := []string{"source_origin", "base_url", "site_name", "platform", "account_label", "credential_type", "credential"}
		for key := range row {
			if !slices.Contains(allowed, key) {
				entry.Problem = "unsupported_field"
			}
		}
		entry.SourceOrigin, entry.BaseURL = stringField(row, "source_origin"), stringField(row, "base_url")
		entry.SiteName, entry.Platform = stringField(row, "site_name"), stringField(row, "platform")
		entry.AccountLabel, entry.CredentialType = stringField(row, "account_label"), stringField(row, "credential_type")
		credential, valid := row["credential"].(map[string]any)
		if !valid {
			entry.Problem = "credential_required"
			return entry
		}
		allowedCredential := []string{"api_key", "access_token", "cookie", "user_id", "refresh_token", "expires_at"}
		for key := range credential {
			if !slices.Contains(allowedCredential, key) {
				entry.Problem = "unsupported_credential_field"
			}
		}
		entry.Credential = provider.Credentials{APIKey: stringField(credential, "api_key"), AccessToken: stringField(credential, "access_token"), Cookie: stringField(credential, "cookie"), RefreshToken: stringField(credential, "refresh_token")}
		entry.RefreshPresent, entry.ExpiryPresent = credential["refresh_token"] != nil, credential["expires_at"] != nil
		if entry.RefreshPresent {
			if _, valid := credential["refresh_token"].(string); !valid {
				entry.Problem = "invalid_credential"
			}
		}
		var err error
		if entry.Credential.UserID, err = browserImportOptionalInt(credential, "user_id"); err != nil {
			entry.Problem = "invalid_entry"
		}
		if entry.Credential.ExpiresAt, err = browserImportOptionalInt(credential, "expires_at"); err != nil {
			entry.Problem = "invalid_entry"
		}
		return entry
	}
	entry.BaseURL, entry.SiteName, entry.Platform = stringField(row, "site_url"), stringField(row, "site_name"), stringField(row, "site_type")
	if base, err := url.Parse(entry.BaseURL); err == nil && base.Host != "" {
		entry.SourceOrigin = base.Scheme + "://" + base.Host
	}
	info, _ := row["account_info"].(map[string]any)
	entry.AccountLabel = stringField(info, "username")
	entry.Credential.UserID, _ = browserImportInt(info["id"])
	entry.CredentialType = stringField(row, "authType")
	switch entry.CredentialType {
	case model.CredentialTypeAccessToken:
		entry.Credential.AccessToken = stringField(info, "access_token")
		if entry.Platform == model.SitePlatformSub2API {
			refresh, _ := row["sub2apiAuth"].(map[string]any)
			entry.Credential.RefreshToken = stringField(refresh, "refreshToken")
			entry.RefreshPresent, entry.ExpiryPresent = refresh["refreshToken"] != nil, refresh["tokenExpiresAt"] != nil
			if entry.RefreshPresent {
				if _, valid := refresh["refreshToken"].(string); !valid {
					entry.Problem = "invalid_credential"
				}
			}
			if entry.ExpiryPresent {
				var err error
				if entry.Credential.ExpiresAt, err = browserImportInt(refresh["tokenExpiresAt"]); err != nil {
					entry.Problem = "invalid_entry"
				}
			}
		}
	case model.CredentialTypeCookie:
		cookie, _ := row["cookieAuth"].(map[string]any)
		entry.Credential.Cookie = stringField(cookie, "sessionCookie")
	default:
		entry.Problem = "unsupported_credential_type"
	}
	return entry
}

func normalizeBrowserImportEntry(entry *browserImportEntry) {
	entry.SiteName, entry.AccountLabel = strings.TrimSpace(entry.SiteName), strings.TrimSpace(entry.AccountLabel)
	entry.Platform = normalizeBrowserImportPlatform(entry.Platform)
	if entry.Platform == "" {
		entry.Problem = "unsupported_platform"
	}
	for _, name := range []*string{&entry.SiteName, &entry.AccountLabel} {
		if *name == "" || !utf8.ValidString(*name) || utf8.RuneCountInString(*name) > 191 || strings.ContainsFunc(*name, unicode.IsControl) {
			*name = ""
			entry.Problem = "invalid_name"
		}
	}
	var err error
	entry.SourceOrigin, err = normalizeBrowserImportURL(entry.SourceOrigin, true)
	if err != nil {
		entry.Problem = "invalid_source_origin"
	}
	entry.BaseURL, err = normalizeBrowserImportURL(entry.BaseURL, false)
	if err != nil {
		entry.Problem = "invalid_base_url"
	}
	if entry.BaseURL != "" && entry.SourceOrigin != "" {
		base, _ := url.Parse(entry.BaseURL)
		if base.Scheme+"://"+base.Host != entry.SourceOrigin {
			entry.Problem = "source_target_mismatch"
		}
	}
	entry.CredentialType = strings.TrimSpace(entry.CredentialType)
	if !slices.Contains([]string{model.CredentialTypeAPIKey, model.CredentialTypeAccessToken, model.CredentialTypeCookie}, entry.CredentialType) {
		entry.CredentialType = ""
		entry.Problem = "unsupported_credential_type"
	}
	credential := &entry.Credential
	credential.APIKey, credential.Cookie = strings.TrimSpace(credential.APIKey), strings.TrimSpace(credential.Cookie)
	credential.AccessToken = strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(credential.AccessToken), "Bearer "))
	credential.RefreshToken = strings.TrimSpace(credential.RefreshToken)
	for _, secret := range []string{credential.APIKey, credential.AccessToken, credential.Cookie, credential.RefreshToken} {
		if len(secret) > browserImportMaxSecret || strings.ContainsFunc(secret, unicode.IsControl) {
			entry.Problem = "invalid_credential"
		}
	}
	if browserImportPrimaryCredential(entry.CredentialType, *credential) == "" {
		entry.Problem = "credential_required"
	}
	if (entry.CredentialType != model.CredentialTypeAPIKey && credential.APIKey != "") || (entry.CredentialType != model.CredentialTypeAccessToken && credential.AccessToken != "") || (entry.CredentialType != model.CredentialTypeCookie && credential.Cookie != "") {
		entry.Problem = "ambiguous_credential"
	}
	if entry.Platform == model.SitePlatformOpenAICompatible && entry.CredentialType != model.CredentialTypeAPIKey || entry.Platform == model.SitePlatformSub2API && entry.CredentialType == model.CredentialTypeCookie {
		entry.Problem = "unsupported_credential_type"
	}
	if credential.UserID < 0 || credential.ExpiresAt < 0 {
		entry.Problem = "invalid_entry"
	}
	if entry.CredentialType == model.CredentialTypeCookie && credential.UserID <= 0 {
		entry.Problem = "user_id_required"
	}
}

func normalizeBrowserImportPlatform(platform string) string {
	switch strings.ToLower(strings.TrimSpace(platform)) {
	case "new-api-family", "new-api", "newapi", "one-api", "one-hub", "done-hub", "voapi", "axon-hub", "axonhub":
		return model.SitePlatformNewAPIFamily
	case "anyrouter":
		return model.SitePlatformAnyRouter
	case "veloera":
		return model.SitePlatformVeloera
	case "sub2api":
		return model.SitePlatformSub2API
	case "openai-compatible", "openai", "openai-compatible-api", "openai_compatible":
		return model.SitePlatformOpenAICompatible
	default:
		return ""
	}
}

func normalizeBrowserImportURL(raw string, origin bool) (string, error) {
	raw = strings.TrimSpace(raw)
	invalid := errors.New("invalid browser import URL")
	if len(raw) > 2048 || strings.ContainsFunc(raw, unicode.IsControl) || strings.Contains(raw, "\\") {
		return "", invalid
	}
	parsed, err := url.Parse(raw)
	if err != nil || parsed.Host == "" || parsed.Opaque != "" || parsed.User != nil || parsed.RawQuery != "" || parsed.ForceQuery || parsed.Fragment != "" || strings.Contains(raw, "#") {
		return "", invalid
	}
	if parsed.Scheme != "http" && parsed.Scheme != "https" || strings.ContainsFunc(parsed.Path, unicode.IsControl) || strings.Contains(parsed.Path, "\\") {
		return "", invalid
	}
	if origin && parsed.Path != "" && parsed.Path != "/" {
		return "", invalid
	}
	for _, part := range strings.Split(parsed.Path, "/") {
		if part == "." || part == ".." {
			return "", invalid
		}
	}
	host, port := strings.ToLower(parsed.Hostname()), parsed.Port()
	if host == "" {
		return "", invalid
	}
	if port != "" {
		number, portErr := strconv.Atoi(port)
		if portErr != nil || number <= 0 || number > 65535 {
			return "", invalid
		}
		if parsed.Scheme == "https" && port == "443" || parsed.Scheme == "http" && port == "80" {
			port = ""
		}
	}
	parsed.Host = host
	if port != "" {
		parsed.Host = net.JoinHostPort(host, port)
	} else if strings.Contains(host, ":") {
		parsed.Host = "[" + host + "]"
	}
	escapedPath, err := browserImportCanonicalEscapedPath(parsed.EscapedPath())
	if err != nil {
		return "", invalid
	}
	escapedPath = strings.TrimRight(escapedPath, "/")
	parsed.Path, err = url.PathUnescape(escapedPath)
	if err != nil {
		return "", invalid
	}
	parsed.RawPath = escapedPath
	if err = provider.ValidateBaseURL(parsed.String(), false); err != nil {
		return "", invalid
	}
	return parsed.String(), nil
}

func browserImportCanonicalEscapedPath(path string) (string, error) {
	const hexDigits = "0123456789ABCDEF"
	var canonical strings.Builder
	canonical.Grow(len(path))
	for index := 0; index < len(path); index++ {
		if path[index] != '%' {
			canonical.WriteByte(path[index])
			continue
		}
		if index+2 >= len(path) {
			return "", errors.New("invalid URL escape")
		}
		value, err := strconv.ParseUint(path[index+1:index+3], 16, 8)
		if err != nil {
			return "", err
		}
		character := byte(value)
		if character >= 'a' && character <= 'z' || character >= 'A' && character <= 'Z' || character >= '0' && character <= '9' || strings.ContainsRune("-._~", rune(character)) {
			canonical.WriteByte(character)
		} else {
			// Reserved bytes stay escaped, so %2F never becomes a path separator.
			// Hex spelling is canonical even when the input uses lowercase digits.
			canonical.WriteByte('%')
			canonical.WriteByte(hexDigits[character>>4])
			canonical.WriteByte(hexDigits[character&15])
		}
		index += 2
	}
	return canonical.String(), nil
}

func browserImportPrimaryCredential(kind string, credential provider.Credentials) string {
	switch kind {
	case model.CredentialTypeAPIKey:
		return credential.APIKey
	case model.CredentialTypeAccessToken:
		return credential.AccessToken
	case model.CredentialTypeCookie:
		return credential.Cookie
	default:
		return ""
	}
}

func markBrowserImportDuplicates(entries []browserImportEntry) {
	seen := map[string]int{}
	identities := map[string]int{}
	for index := range entries {
		entry := &entries[index]
		if entry.Problem != "" {
			continue
		}
		secret := sha256.Sum256([]byte(browserImportPrimaryCredential(entry.CredentialType, entry.Credential)))
		key := entry.BaseURL + "\x00" + entry.CredentialType + "\x00" + hex.EncodeToString(secret[:])
		if previous, exists := seen[key]; exists {
			entry.Problem, entry.DuplicateOf = "duplicate_identity", &previous
			continue
		}
		seen[key] = index
		if entry.Credential.UserID > 0 && entry.CredentialType != model.CredentialTypeAPIKey {
			identity := entry.BaseURL + "\x00" + strconv.FormatInt(entry.Credential.UserID, 10)
			if previous, exists := identities[identity]; exists {
				entry.Problem, entries[previous].Problem = "ambiguous_source_identity", "ambiguous_source_identity"
				entry.DuplicateOf = &previous
			} else {
				identities[identity] = index
			}
		}
	}
}

func stringField(value map[string]any, field string) string {
	text, _ := value[field].(string)
	return text
}

func browserImportOptionalInt(value map[string]any, field string) (int64, error) {
	if value[field] == nil {
		return 0, nil
	}
	return browserImportInt(value[field])
}

func browserImportInt(value any) (int64, error) {
	switch number := value.(type) {
	case json.Number:
		return strconv.ParseInt(string(number), 10, 64)
	case string:
		return strconv.ParseInt(strings.TrimSpace(number), 10, 64)
	default:
		return 0, errors.New("invalid integer")
	}
}

// Parse tokens with bounded depth and unique object keys. No error contains an
// input token or credential; unrecognized external sections remain unconsumed.
func decodeBrowserImportJSON(raw []byte) (any, error) {
	if !utf8.Valid(raw) || !browserImportValidUnicodeEscapes(raw) {
		return nil, previewFailure("", "browser_import_invalid_document")
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.UseNumber()
	value, err := readBrowserImportJSONValue(decoder, 0)
	if err != nil {
		return nil, previewFailure("", "browser_import_invalid_document")
	}
	if _, err = decoder.Token(); err != io.EOF {
		return nil, previewFailure("", "browser_import_invalid_document")
	}
	return value, nil
}

// encoding/json replaces lone UTF-16 surrogates even when the source bytes are
// valid UTF-8. Reject those escapes before decoding without rejecting literal
// U+FFFD, valid surrogate pairs, or a literal backslash followed by "u".
func browserImportValidUnicodeEscapes(raw []byte) bool {
	inString := false
	for index := 0; index < len(raw); index++ {
		if raw[index] == '"' {
			inString = !inString
			continue
		}
		if !inString || raw[index] != '\\' {
			continue
		}
		index++
		if index >= len(raw) {
			return false
		}
		if raw[index] != 'u' {
			continue // The JSON decoder checks the other escape spellings.
		}
		if index+4 >= len(raw) {
			return false
		}
		code, err := strconv.ParseUint(string(raw[index+1:index+5]), 16, 16)
		if err != nil {
			return false
		}
		index += 4
		switch {
		case code >= 0xd800 && code <= 0xdbff:
			if index+6 >= len(raw) || raw[index+1] != '\\' || raw[index+2] != 'u' {
				return false
			}
			low, err := strconv.ParseUint(string(raw[index+3:index+7]), 16, 16)
			if err != nil || low < 0xdc00 || low > 0xdfff {
				return false
			}
			index += 6
		case code >= 0xdc00 && code <= 0xdfff:
			return false
		}
	}
	return !inString
}

func readBrowserImportJSONValue(decoder *json.Decoder, depth int) (any, error) {
	if depth > 32 {
		return nil, errors.New("JSON depth limit")
	}
	token, err := decoder.Token()
	if err != nil {
		return nil, err
	}
	delimiter, container := token.(json.Delim)
	if !container {
		return token, nil
	}
	switch delimiter {
	case '{':
		object := map[string]any{}
		for decoder.More() {
			keyToken, keyErr := decoder.Token()
			if keyErr != nil {
				return nil, keyErr
			}
			key, valid := keyToken.(string)
			if !valid {
				return nil, errors.New("invalid object key")
			}
			if _, exists := object[key]; exists {
				return nil, errors.New("duplicate object key")
			}
			value, valueErr := readBrowserImportJSONValue(decoder, depth+1)
			if valueErr != nil {
				return nil, valueErr
			}
			object[key] = value
		}
		if end, endErr := decoder.Token(); endErr != nil || end != json.Delim('}') {
			return nil, errors.New("invalid object end")
		}
		return object, nil
	case '[':
		array := []any{}
		for decoder.More() {
			value, valueErr := readBrowserImportJSONValue(decoder, depth+1)
			if valueErr != nil {
				return nil, valueErr
			}
			array = append(array, value)
		}
		if end, endErr := decoder.Token(); endErr != nil || end != json.Delim(']') {
			return nil, errors.New("invalid array end")
		}
		return array, nil
	default:
		return nil, errors.New("invalid JSON container")
	}
}
