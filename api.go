package main

import (
	"bytes"
	"context"
	"crypto/tls"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"strings"
	"sync"
	"time"
)

const (
	defaultAPIBase = "https://pixeldrain.com/api"
	appName        = "Pixeldrain Desktop"
	userAgent      = "PixeldrainDesktop/1.0"
)

// pdFile is the file info object of the pixeldrain API.
type pdFile struct {
	ID                  string `json:"id"`
	Name                string `json:"name"`
	Size                int64  `json:"size"`
	Views               int64  `json:"views"`
	Downloads           int64  `json:"downloads"`
	BandwidthUsed       int64  `json:"bandwidth_used"`
	BandwidthUsedPaid   int64  `json:"bandwidth_used_paid"`
	MimeType            string `json:"mime_type"`
	HashSHA256          string `json:"hash_sha256"`
	DateUpload          string `json:"date_upload"`
	DateLastView        string `json:"date_last_view"`
	DeleteAfterDate     string `json:"delete_after_date"`
	Availability        string `json:"availability"`
	AvailabilityMessage string `json:"availability_message"`
	CanEdit             bool   `json:"can_edit"`
	Description         string `json:"description"`
}

type pdSubscription struct {
	ID                     string `json:"id"`
	Name                   string `json:"name"`
	Type                   string `json:"type"`
	FileSizeLimit          int64  `json:"file_size_limit"`
	FileExpiryDays         int    `json:"file_expiry_days"`
	StorageSpace           int64  `json:"storage_space"`
	MonthlyTransferCap     int64  `json:"monthly_transfer_cap"`
	FilesystemAccess       bool   `json:"filesystem_access"`
	FilesystemStorageLimit int64  `json:"filesystem_storage_limit"`
}

type pdUser struct {
	Username              string         `json:"username"`
	Email                 string         `json:"email"`
	CanUpload             *bool          `json:"can_upload"`
	Subscription          pdSubscription `json:"subscription"`
	StorageSpaceUsed      int64          `json:"storage_space_used"`
	FilesystemStorageUsed int64          `json:"filesystem_storage_used"`
	FileCount             int            `json:"file_count"`
	MonthlyTransferCap    int64          `json:"monthly_transfer_cap"`
	MonthlyTransferUsed   int64          `json:"monthly_transfer_used"`
	BalanceMicroEUR       int64          `json:"balance_micro_eur"`
}

type pdListSummary struct {
	ID          string `json:"id"`
	Title       string `json:"title"`
	DateCreated string `json:"date_created"`
	FileCount   int    `json:"file_count"`
	CanEdit     bool   `json:"can_edit"`
}

type pdList struct {
	pdListSummary
	Files []pdFile `json:"files"`
}

// pdNode is a filesystem node. Paths include the bucket ("/me/...").
type pdNode struct {
	Type     string `json:"type"` // file | dir
	Path     string `json:"path"`
	Name     string `json:"name"`
	Created  string `json:"created"`
	Modified string `json:"modified"`
	FileSize int64  `json:"file_size"`
	FileType string `json:"file_type"`
	SHA256   string `json:"sha256_sum"`
	ID       string `json:"id"`
}

type pdStat struct {
	Path        []pdNode `json:"path"`
	BaseIndex   int      `json:"base_index"`
	Children    []pdNode `json:"children"`
	Permissions struct {
		Owner  bool `json:"owner"`
		Read   bool `json:"read"`
		Write  bool `json:"write"`
		Delete bool `json:"delete"`
	} `json:"permissions"`
}

func (s *pdStat) node() pdNode {
	if s.BaseIndex >= 0 && s.BaseIndex < len(s.Path) {
		return s.Path[s.BaseIndex]
	}
	return pdNode{}
}

// apiError is a non-2xx response. Value is pixeldrain's stable error code.
type apiError struct {
	Status  int
	Value   string
	Message string
}

func (e *apiError) Error() string {
	if msg, ok := errorMessages[e.Value]; ok {
		return L(msg[0], msg[1])
	}
	if e.Message != "" {
		return fmt.Sprintf("%s (HTTP %d)", e.Message, e.Status)
	}
	if e.Value != "" {
		return fmt.Sprintf("%s (HTTP %d)", e.Value, e.Status)
	}
	return fmt.Sprintf(L("서버 오류 (HTTP %d)", "Server error (HTTP %d)"), e.Status)
}

// errorMessages translates the error codes users actually run into.
var errorMessages = map[string][2]string{
	"authentication_required":              {"로그인이 필요합니다", "Sign-in required"},
	"authentication_failed":                {"API 키가 올바르지 않거나 만료되었습니다", "The API key is invalid or has expired"},
	"forbidden":                            {"이 작업을 할 권한이 없습니다", "You are not allowed to do this"},
	"not_found":                            {"파일을 찾을 수 없습니다", "File not found"},
	"path_not_found":                       {"경로를 찾을 수 없습니다", "Path not found"},
	"permission_denied":                    {"이 경로에 대한 권한이 없습니다", "You don't have permission for this path"},
	"user_not_found":                       {"계정을 찾을 수 없습니다", "Account not found"},
	"password_incorrect":                   {"비밀번호가 올바르지 않습니다", "Incorrect password"},
	"otp_incorrect":                        {"인증 코드가 올바르지 않습니다", "Incorrect verification code"},
	"otp_required":                         {"2단계 인증 코드가 필요합니다", "A two-factor authentication code is required"},
	"ip_rate_limit_reached":                {"요청이 너무 많습니다. 잠시 후 다시 시도하세요", "Too many requests. Try again in a moment"},
	"read_only_mode_enabled":               {"pixeldrain이 읽기 전용 모드입니다. 잠시 후 다시 시도하세요", "pixeldrain is in read-only mode. Try again later"},
	"file_too_large":                       {"파일이 요금제의 최대 파일 크기를 넘습니다", "The file exceeds your plan's maximum file size"},
	"user_out_of_space":                    {"저장 공간이 부족합니다", "Not enough storage space"},
	"out_of_transfer":                      {"이번 달 전송량을 모두 사용했습니다", "You have used up this month's transfer"},
	"name_contains_illegal_character":      {"파일 이름에 사용할 수 없는 문자가 있습니다", "The file name contains characters that aren't allowed"},
	"name_too_long":                        {"이름이 너무 깁니다 (최대 255바이트)", "Name is too long (max 255 bytes)"},
	"too_many_files":                       {"파일 개수 한도에 도달했습니다", "File count limit reached"},
	"ip_banned":                            {"이 IP는 업로드가 차단되었습니다", "Uploads are blocked for this IP"},
	"account_banned":                       {"계정이 차단되었습니다", "This account is banned"},
	"email_address_not_verified":           {"업로드하려면 먼저 이메일 주소를 인증하세요", "Verify your e-mail address before uploading"},
	"node_already_exists":                  {"같은 이름의 항목이 이미 있습니다", "An item with the same name already exists"},
	"directory_not_empty":                  {"폴더가 비어 있지 않습니다", "The folder is not empty"},
	"file_rate_limited_captcha_required":   {"다운로드 제한: 브라우저에서 CAPTCHA를 풀어야 합니다", "Download limited: solve the CAPTCHA in a browser"},
	"virus_detected_captcha_required":      {"악성 파일 경고: 브라우저에서 확인 후 받으세요", "Malware warning: confirm in a browser before downloading"},
	"ip_download_limited_captcha_required": {"IP 다운로드 한도 도달: 브라우저에서 CAPTCHA를 풀어야 합니다", "IP download limit reached: solve the CAPTCHA in a browser"},
	"server_overload_captcha_required":     {"서버 과부하: 브라우저에서 CAPTCHA를 풀어야 합니다", "Server overloaded: solve the CAPTCHA in a browser"},
	"hotlink_detected":                     {"핫링크가 차단되었습니다. 프리미엄 계정으로 로그인하세요", "Hotlinking is blocked. Sign in with a premium account"},
	"max_concurrent_downloads":             {"동시 다운로드 한도에 도달했습니다", "Concurrent download limit reached"},
	"transfer_limit_exceeded":              {"전송 한도를 넘었습니다. 한도가 초기화될 때까지 기다리세요", "Transfer limit exceeded. Wait until it resets"},
	"download_limit_exceeded":              {"다운로드 한도를 넘었습니다. 한도가 초기화될 때까지 기다리세요", "Download limit exceeded. Wait until it resets"},
	"unavailable_for_legal_reasons":        {"법적 사유로 제공되지 않는 파일입니다", "This file is unavailable for legal reasons"},
	"list_file_not_found":                  {"목록에 넣을 파일 중 일부를 찾을 수 없습니다", "Some files for the list could not be found"},
	"cannot_create_empty_list":             {"빈 목록은 만들 수 없습니다", "An empty list can't be created"},
	"no_login_method_available":            {"비밀번호가 설정되지 않은 계정입니다. API 키로 로그인하세요", "This account has no password. Sign in with an API key"},
	"login_link_already_sent":              {"로그인 링크가 이미 이메일로 전송되었습니다", "A login link was already sent by e-mail"},
	"request_origin_invalid":               {"pixeldrain이 이 로그인 요청을 거부했습니다. API 키로 로그인하세요", "pixeldrain rejected this login request. Sign in with an API key"},
}

func errValue(err error) string {
	var ae *apiError
	if errors.As(err, &ae) {
		return ae.Value
	}
	return ""
}

func isUnauthorized(err error) bool {
	var ae *apiError
	return errors.As(err, &ae) && ae.Status == http.StatusUnauthorized
}

// Client talks to the pixeldrain API. The API key is only ever attached to
// requests for the configured API host.
type Client struct {
	mu     sync.RWMutex
	base   *url.URL
	key    string
	http   *http.Client // short requests
	stream *http.Client // uploads and downloads, no overall timeout
}

func newClient() *Client {
	raw := strings.TrimRight(os.Getenv("PIXELDRAIN_API_BASE"), "/")
	if raw == "" {
		raw = defaultAPIBase
	}
	base, err := url.Parse(raw)
	if err != nil {
		base, _ = url.Parse(defaultAPIBase)
	}
	// HTTP/1.1 with large buffers: Go's HTTP/2 client is flow-control bound on
	// single large uploads, while one HTTP/1.1 connection per transfer is not.
	tr := &http.Transport{
		Proxy:                 http.ProxyFromEnvironment,
		DialContext:           (&net.Dialer{Timeout: 30 * time.Second, KeepAlive: 30 * time.Second}).DialContext,
		TLSHandshakeTimeout:   20 * time.Second,
		ResponseHeaderTimeout: 0,
		MaxIdleConnsPerHost:   8,
		IdleConnTimeout:       90 * time.Second,
		WriteBufferSize:       256 << 10,
		ReadBufferSize:        256 << 10,
		ForceAttemptHTTP2:     false,
		TLSNextProto:          map[string]func(string, *tls.Conn) http.RoundTripper{},
	}
	return &Client{
		base:   base,
		http:   &http.Client{Timeout: 90 * time.Second},
		stream: &http.Client{Transport: tr},
	}
}

func (c *Client) setKey(k string) {
	c.mu.Lock()
	c.key = strings.TrimSpace(k)
	c.mu.Unlock()
}

func (c *Client) apiKey() string {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.key
}

// siteURL is the public website root (https://pixeldrain.com) used for share links.
func (c *Client) siteURL() string {
	u := *c.base
	u.Path = strings.TrimSuffix(u.Path, "/api")
	u.RawQuery = ""
	return strings.TrimRight(u.String(), "/")
}

func (c *Client) endpoint(p string, q url.Values) string {
	u := *c.base
	// Leaving RawPath empty makes net/url escape '?', '#', '%' and spaces in
	// names while keeping ',' (used for multi-file requests) as-is.
	u.Path = strings.TrimRight(u.Path, "/") + p
	u.RawPath = ""
	if q != nil {
		u.RawQuery = q.Encode()
	}
	return u.String()
}

// authorize attaches the API key, but only for the API host itself.
func (c *Client) authorize(req *http.Request) {
	req.Header.Set("User-Agent", userAgent)
	if key := c.apiKey(); key != "" && strings.EqualFold(req.URL.Host, c.base.Host) && req.URL.Scheme == c.base.Scheme {
		req.SetBasicAuth("", key)
	}
}

func (c *Client) newRequest(ctx context.Context, method, endpoint string, body io.Reader) (*http.Request, error) {
	req, err := http.NewRequestWithContext(ctx, method, endpoint, body)
	if err != nil {
		return nil, err
	}
	c.authorize(req)
	return req, nil
}

func readAPIError(res *http.Response) error {
	b, _ := io.ReadAll(io.LimitReader(res.Body, 64<<10))
	ae := &apiError{Status: res.StatusCode}
	var body struct {
		Value   string `json:"value"`
		Message string `json:"message"`
	}
	if json.Unmarshal(b, &body) == nil {
		ae.Value, ae.Message = body.Value, body.Message
	} else if s := strings.TrimSpace(string(b)); len(s) < 200 {
		ae.Message = s
	}
	return ae
}

func (c *Client) do(ctx context.Context, method, p string, q url.Values, body io.Reader, contentType string, out any) error {
	req, err := c.newRequest(ctx, method, c.endpoint(p, q), body)
	if err != nil {
		return err
	}
	if contentType != "" {
		req.Header.Set("Content-Type", contentType)
	}
	res, err := c.http.Do(req)
	if err != nil {
		return err
	}
	defer res.Body.Close()
	if res.StatusCode >= 300 {
		return readAPIError(res)
	}
	if out == nil {
		_, _ = io.Copy(io.Discard, res.Body)
		return nil
	}
	return json.NewDecoder(res.Body).Decode(out)
}

func (c *Client) get(ctx context.Context, p string, q url.Values, out any) error {
	return c.do(ctx, http.MethodGet, p, q, nil, "", out)
}

func (c *Client) postForm(ctx context.Context, p string, form url.Values, out any) error {
	return c.do(ctx, http.MethodPost, p, nil, strings.NewReader(form.Encode()), "application/x-www-form-urlencoded", out)
}

// ---------------------------------------------------------------- account

func (c *Client) User(ctx context.Context) (*pdUser, error) {
	var u pdUser
	if err := c.get(ctx, "/user", nil, &u); err != nil {
		return nil, err
	}
	return &u, nil
}

// Login exchanges account credentials for a new API key.
func (c *Client) Login(ctx context.Context, username, password, totp string) (string, error) {
	form := url.Values{"username": {username}, "password": {password}, "app_name": {appName}}
	if totp != "" {
		form.Set("totp", totp)
	}
	var out struct {
		AuthKey string `json:"auth_key"`
		Value   string `json:"value"`
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.endpoint("/user/login", nil), strings.NewReader(form.Encode()))
	if err != nil {
		return "", err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("User-Agent", userAgent)
	res, err := c.http.Do(req)
	if err != nil {
		return "", err
	}
	defer res.Body.Close()
	if res.StatusCode == http.StatusAccepted {
		return "", &apiError{Status: res.StatusCode, Value: "login_link_sent"}
	}
	if res.StatusCode >= 300 {
		return "", readAPIError(res)
	}
	if err := json.NewDecoder(res.Body).Decode(&out); err != nil {
		return "", err
	}
	if out.AuthKey == "" {
		return "", newError("로그인 응답에 API 키가 없습니다", "The login response contained no API key")
	}
	return out.AuthKey, nil
}

// Logout revokes the current key on the server.
func (c *Client) Logout(ctx context.Context) error {
	return c.do(ctx, http.MethodDelete, "/user/session", nil, nil, "", nil)
}

// ------------------------------------------------------------------ files

func (c *Client) UserFiles(ctx context.Context) ([]pdFile, error) {
	var out struct {
		Files []pdFile `json:"files"`
	}
	if err := c.get(ctx, "/user/files", nil, &out); err != nil {
		return nil, err
	}
	return out.Files, nil
}

// FileInfo fetches info for many files, 100 per request. Missing files are
// silently omitted, like the API does.
func (c *Client) FileInfo(ctx context.Context, ids []string) ([]pdFile, error) {
	var all []pdFile
	for len(ids) > 0 {
		n := min(len(ids), 100)
		chunk := ids[:n]
		ids = ids[n:]
		var raw json.RawMessage
		if err := c.get(ctx, "/file/"+strings.Join(chunk, ",")+"/info", nil, &raw); err != nil {
			if errValue(err) == "not_found" && len(chunk) > 1 {
				continue
			}
			return nil, err
		}
		raw = bytes.TrimSpace(raw)
		if len(raw) > 0 && raw[0] == '[' {
			var fs []pdFile
			if err := json.Unmarshal(raw, &fs); err != nil {
				return nil, err
			}
			all = append(all, fs...)
		} else {
			var f pdFile
			if err := json.Unmarshal(raw, &f); err != nil {
				return nil, err
			}
			all = append(all, f)
		}
	}
	return all, nil
}

func (c *Client) DeleteFile(ctx context.Context, id string) error {
	return c.do(ctx, http.MethodDelete, "/file/"+id, nil, nil, "", nil)
}

// UploadFile streams body as the raw request body (PUT /file/{name}).
func (c *Client) UploadFile(ctx context.Context, name string, body io.Reader, size int64) (*pdFile, error) {
	req, err := c.newRequest(ctx, http.MethodPut, c.endpoint("/file/"+name, nil), body)
	if err != nil {
		return nil, err
	}
	setBody(req, body, size)
	res, err := c.stream.Do(req)
	if err != nil {
		return nil, err
	}
	defer res.Body.Close()
	if res.StatusCode >= 300 {
		return nil, readAPIError(res)
	}
	var f pdFile
	if err := json.NewDecoder(res.Body).Decode(&f); err != nil {
		return nil, fmt.Errorf(L("업로드 응답을 읽지 못했습니다: %w", "Could not read the upload response: %w"), err)
	}
	if f.ID == "" {
		return nil, newError("업로드 응답에 파일 ID가 없습니다", "The upload response contained no file ID")
	}
	if f.Name == "" {
		f.Name = name
	}
	if f.Size == 0 {
		f.Size = size
	}
	return &f, nil
}

func setBody(req *http.Request, body io.Reader, size int64) {
	req.Header.Set("Content-Type", "application/octet-stream")
	req.ContentLength = size
	if size == 0 {
		req.Body = http.NoBody
	}
	// Never replay a streamed body on redirects.
	req.GetBody = nil
}

// ------------------------------------------------------------------ lists

func (c *Client) UserLists(ctx context.Context) ([]pdListSummary, error) {
	var out struct {
		Lists []pdListSummary `json:"lists"`
	}
	if err := c.get(ctx, "/user/lists", nil, &out); err != nil {
		return nil, err
	}
	return out.Lists, nil
}

func (c *Client) GetList(ctx context.Context, id string) (*pdList, error) {
	var l pdList
	if err := c.get(ctx, "/list/"+id, nil, &l); err != nil {
		return nil, err
	}
	return &l, nil
}

func (c *Client) CreateList(ctx context.Context, title string, ids []string) (string, error) {
	type entry struct {
		ID string `json:"id"`
	}
	files := make([]entry, len(ids))
	for i, id := range ids {
		files[i] = entry{ID: id}
	}
	b, _ := json.Marshal(map[string]any{"title": title, "anonymous": false, "files": files})
	var out struct {
		ID string `json:"id"`
	}
	if err := c.do(ctx, http.MethodPost, "/list", nil, bytes.NewReader(b), "application/json", &out); err != nil {
		return "", err
	}
	return out.ID, nil
}

// ------------------------------------------------------------- filesystem

func fsEndpoint(p string) string { return "/filesystem" + cleanFSPath(p) }

func (c *Client) FSStat(ctx context.Context, p string) (*pdStat, error) {
	var st pdStat
	if err := c.get(ctx, fsEndpoint(p), url.Values{"stat": {""}}, &st); err != nil {
		return nil, err
	}
	return &st, nil
}

// FSPut writes body to the filesystem path, creating parent directories.
func (c *Client) FSPut(ctx context.Context, p string, body io.Reader, size int64) (*pdNode, error) {
	req, err := c.newRequest(ctx, http.MethodPut, c.endpoint(fsEndpoint(p), url.Values{"make_parents": {"true"}}), body)
	if err != nil {
		return nil, err
	}
	setBody(req, body, size)
	res, err := c.stream.Do(req)
	if err != nil {
		return nil, err
	}
	defer res.Body.Close()
	if res.StatusCode >= 300 {
		return nil, readAPIError(res)
	}
	var n pdNode
	if err := json.NewDecoder(io.LimitReader(res.Body, 1<<20)).Decode(&n); err != nil {
		return nil, fmt.Errorf("invalid filesystem upload response: %w", err)
	}
	if n.Type != "file" || n.FileSize != size {
		return nil, newError("파일시스템 업로드 응답의 파일 정보가 올바르지 않습니다", "Invalid file metadata in the filesystem upload response")
	}
	return &n, nil
}

func (c *Client) FSAction(ctx context.Context, p string, form url.Values) error {
	return c.postForm(ctx, fsEndpoint(p), form, nil)
}

func (c *Client) FSDelete(ctx context.Context, p string, recursive bool) error {
	var q url.Values
	if recursive {
		q = url.Values{"recursive": {""}}
	}
	return c.do(ctx, http.MethodDelete, fsEndpoint(p), q, nil, "", nil)
}

// ------------------------------------------------------------------ bytes

// fileURL is the raw download URL of a file.
func (c *Client) fileURL(id string) string {
	return c.endpoint("/file/"+id, url.Values{"download": {""}})
}

func (c *Client) fsURL(p string) string { return c.endpoint(fsEndpoint(p), nil) }

// Open requests raw bytes starting at offset (Range) from an API URL.
func (c *Client) Open(ctx context.Context, rawURL string, offset int64, rangeHeader string) (*http.Response, error) {
	req, err := c.newRequest(ctx, http.MethodGet, rawURL, nil)
	if err != nil {
		return nil, err
	}
	switch {
	case rangeHeader != "":
		req.Header.Set("Range", rangeHeader)
	case offset > 0:
		req.Header.Set("Range", fmt.Sprintf("bytes=%d-", offset))
	}
	res, err := c.stream.Do(req)
	if err != nil {
		return nil, err
	}
	if res.StatusCode >= 300 {
		defer res.Body.Close()
		return nil, readAPIError(res)
	}
	return res, nil
}
