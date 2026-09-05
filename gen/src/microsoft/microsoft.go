// Package microsoft resolves Microsoft-backed Minecraft account auth into the
// existing go-mc bot login inputs.
//
// The intended workflow is split into two entrypoints:
//
//  1. AuthenticateDeviceCode for interactive device-code bootstrap or cache repair.
//  2. AuthenticateCached for non-interactive cached-token reuse in automated runs.
//
// Both return the same Access result so callers can keep the normal online-mode
// join path and avoid special-case packet/login logic.
package microsoft

import (
	"bytes"
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/cookiejar"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"
)

const (
	DefaultClientID   = "00000000441cc96b"
	defaultDeviceType = "Nintendo"
	defaultUserAgent  = "MinecraftLauncher/2.2.10675"

	liveDeviceCodeURL    = "https://login.live.com/oauth20_connect.srf"
	liveTokenURL         = "https://login.live.com/oauth20_token.srf"
	xboxUserAuthURL      = "https://user.auth.xboxlive.com/user/authenticate"
	xboxDeviceAuthURL    = "https://device.auth.xboxlive.com/device/authenticate"
	xboxTitleAuthURL     = "https://title.auth.xboxlive.com/title/authenticate"
	xboxXSTSAuthorizeURL = "https://xsts.auth.xboxlive.com/xsts/authorize"
	mcLoginWithXboxURL   = "https://api.minecraftservices.com/authentication/login_with_xbox"
	mcProfileURL         = "https://api.minecraftservices.com/minecraft/profile"
)

var xboxLiveErrors = map[int64]string{
	2148916227: "your account was banned by Xbox for violating one or more community standards",
	2148916229: "your account is currently restricted and cannot play online",
	2148916233: "your account does not have an Xbox profile yet",
	2148916234: "your account has not accepted Xbox terms of service",
	2148916235: "your account resides in a region Xbox has not authorized",
	2148916236: "your account requires proof of age",
	2148916237: "your account has reached its playtime limit",
	2148916238: "the account is under 18 and must be added to a family by an adult",
}

var ErrInteractiveLoginRequired = errors.New("microsoft auth requires interactive device-code approval")

type Options struct {
	ClientID      string
	DeviceType    string
	DeviceVersion string
	HTTPClient    *http.Client
	CacheDir      string
	CacheKey      string
	ForceRefresh  bool
	OnDeviceCode  func(DeviceCode)
	OnStatus      func(string)
}

type DeviceCode struct {
	UserCode        string `json:"user_code"`
	DeviceCode      string `json:"device_code"`
	VerificationURI string `json:"verification_uri"`
	ExpiresIn       int    `json:"expires_in"`
	Interval        int    `json:"interval"`
	Message         string `json:"message"`
}

type Profile struct {
	ID    string `json:"id"`
	Name  string `json:"name"`
	Skins []any  `json:"skins,omitempty"`
	Capes []any  `json:"capes,omitempty"`
}

type Access struct {
	Token   string
	Profile Profile
}

func (a *Access) SelectedProfile() (ID, Name string) {
	return a.Profile.ID, a.Profile.Name
}

func (a *Access) AccessToken() string {
	return a.Token
}

type liveTokenResponse struct {
	AccessToken  string `json:"access_token"`
	RefreshToken string `json:"refresh_token"`
	ExpiresIn    int    `json:"expires_in"`
	Error        string `json:"error"`
	Description  string `json:"error_description"`
}

type xboxUserTokenResponse struct {
	IssueInstant string `json:"IssueInstant"`
	NotAfter     string `json:"NotAfter"`
	Token        string `json:"Token"`
}

type xboxDisplayClaims struct {
	XUI []struct {
		UHS string `json:"uhs"`
		XID string `json:"xid"`
	} `json:"xui"`
}

type xboxXSTSResponse struct {
	IssueInstant  string            `json:"IssueInstant"`
	NotAfter      string            `json:"NotAfter"`
	Token         string            `json:"Token"`
	DisplayClaims xboxDisplayClaims `json:"DisplayClaims"`
	XErr          int64             `json:"XErr,omitempty"`
	Message       string            `json:"Message,omitempty"`
}

type minecraftAuthResponse struct {
	Username    string `json:"username"`
	AccessToken string `json:"access_token"`
	ExpiresIn   int    `json:"expires_in"`
	TokenType   string `json:"token_type"`
	Error       string `json:"error,omitempty"`
	Path        string `json:"path,omitempty"`
}

type flow struct {
	httpClient *http.Client
	clientID   string
	deviceType string
	deviceVers string
	cache      *tokenCache
	forceAuth  bool
	onCode     func(DeviceCode)
	onStatus   func(string)
	key        *ecdsa.PrivateKey
	jwk        jwk
}

type jwk struct {
	KTY string `json:"kty"`
	X   string `json:"x"`
	Y   string `json:"y"`
	CRV string `json:"crv"`
	Alg string `json:"alg"`
	Use string `json:"use"`
}

type xboxXSTS struct {
	UserHash string
	UserXUID string
	Token    string
}

func AuthenticateDeviceCode(ctx context.Context, opts Options) (*Access, error) {
	return authenticate(ctx, opts, true)
}

func AuthenticateCached(ctx context.Context, opts Options) (*Access, error) {
	return authenticate(ctx, opts, false)
}

func authenticate(ctx context.Context, opts Options, allowInteractive bool) (*Access, error) {
	flow, err := newFlow(opts)
	if err != nil {
		return nil, err
	}

	msaToken, err := flow.acquireLiveToken(ctx, allowInteractive)
	if err != nil {
		return nil, err
	}

	flow.status("requesting Xbox user token")
	userToken, err := flow.getUserToken(ctx, msaToken.AccessToken)
	if err != nil {
		return nil, err
	}

	flow.status("requesting Xbox device token")
	deviceToken, err := flow.getDeviceToken(ctx)
	if err != nil {
		return nil, err
	}

	flow.status("requesting Xbox title token")
	titleToken, err := flow.getTitleToken(ctx, msaToken.AccessToken, deviceToken)
	if err != nil {
		return nil, err
	}

	flow.status("requesting Xbox XSTS token")
	xsts, err := flow.getXSTSToken(ctx, userToken, deviceToken, titleToken)
	if err != nil {
		return nil, err
	}

	flow.status("requesting Minecraft access token")
	mcToken, err := flow.loginWithXbox(ctx, xsts)
	if err != nil {
		return nil, err
	}

	flow.status("requesting Minecraft profile")
	profile, err := flow.fetchProfile(ctx, mcToken)
	if err != nil {
		return nil, err
	}
	_ = flow.saveProfile(profile)

	flow.status("resolved Minecraft profile")
	return &Access{Token: mcToken, Profile: profile}, nil
}

func newFlow(opts Options) (*flow, error) {
	clientID := opts.ClientID
	if clientID == "" {
		clientID = DefaultClientID
	}
	deviceType := opts.DeviceType
	if deviceType == "" {
		deviceType = defaultDeviceType
	}
	deviceVers := opts.DeviceVersion
	if deviceVers == "" {
		deviceVers = "0.0.0"
	}
	httpClient := opts.HTTPClient
	if httpClient == nil {
		jar, err := cookiejar.New(nil)
		if err != nil {
			return nil, err
		}
		httpClient = &http.Client{Jar: jar, Timeout: 30 * time.Second}
	}
	cache, err := newTokenCache(opts.CacheDir, opts.CacheKey, clientID)
	if err != nil {
		return nil, err
	}
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return nil, fmt.Errorf("generate xbox signing key: %w", err)
	}
	return &flow{
		httpClient: httpClient,
		clientID:   clientID,
		deviceType: deviceType,
		deviceVers: deviceVers,
		cache:      cache,
		forceAuth:  opts.ForceRefresh,
		onCode:     opts.OnDeviceCode,
		onStatus:   opts.OnStatus,
		key:        key,
		jwk:        publicJWK(key),
	}, nil
}

func (f *flow) acquireLiveToken(ctx context.Context, allowInteractive bool) (*liveTokenResponse, error) {
	if !f.forceAuth {
		token, err := f.acquireCachedLiveToken(ctx)
		if err != nil {
			return nil, err
		}
		if token != nil {
			return token, nil
		}
	}
	if !allowInteractive {
		return nil, ErrInteractiveLoginRequired
	}

	form := url.Values{}
	form.Set("scope", "service::user.auth.xboxlive.com::MBI_SSL")
	form.Set("client_id", f.clientID)
	form.Set("response_type", "device_code")

	var code DeviceCode
	if err := f.doFormJSON(ctx, http.MethodPost, liveDeviceCodeURL, form, nil, &code); err != nil {
		return nil, err
	}
	code.Message = fmt.Sprintf("To sign in, use a web browser to open the page %s and use the code %s or visit http://microsoft.com/link?otc=%s", code.VerificationURI, code.UserCode, code.UserCode)
	if f.onCode != nil {
		f.onCode(code)
	}
	f.status("waiting for Microsoft device-code approval")

	deadline := time.Now().Add(time.Duration(code.ExpiresIn) * time.Second)
	interval := time.Duration(code.Interval) * time.Second
	if interval <= 0 {
		interval = 5 * time.Second
	}

	for time.Now().Before(deadline) {
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-time.After(interval):
		}

		pollForm := url.Values{}
		pollForm.Set("client_id", f.clientID)
		pollForm.Set("device_code", code.DeviceCode)
		pollForm.Set("grant_type", "urn:ietf:params:oauth:grant-type:device_code")

		var token liveTokenResponse
		tokenURL := liveTokenURL + "?client_id=" + url.QueryEscape(f.clientID)
		status, err := f.doFormJSONAnyStatus(ctx, http.MethodPost, tokenURL, pollForm, nil, &token)
		if err != nil {
			return nil, err
		}
		if status == http.StatusBadRequest && token.Error == "" {
			return nil, fmt.Errorf("microsoft device login polling failed with empty error response")
		}
		if token.Error == "authorization_pending" {
			continue
		}
		if token.Error == "slow_down" {
			interval += 5 * time.Second
			continue
		}
		if token.Error != "" {
			return nil, fmt.Errorf("microsoft device login failed (%s): %s", token.Error, token.Description)
		}
		if token.AccessToken == "" {
			continue
		}
		_ = f.saveLiveToken(token)
		f.status("received Microsoft access token")
		return &token, nil
	}

	return nil, fmt.Errorf("microsoft device login timed out")
}

func (f *flow) acquireCachedLiveToken(ctx context.Context) (*liveTokenResponse, error) {
	if f.cache == nil {
		return nil, nil
	}
	state, err := f.cache.load()
	if err != nil {
		return nil, fmt.Errorf("load Microsoft token cache: %w", err)
	}
	if state == nil || state.ClientID != f.clientID {
		return nil, nil
	}
	if liveTokenStillValid(state) {
		f.status("using cached Microsoft access token")
		return &state.Token, nil
	}
	if state.Token.RefreshToken == "" {
		return nil, nil
	}
	f.status("refreshing cached Microsoft token")
	token, err := f.refreshLiveToken(ctx, state.Token.RefreshToken)
	if err != nil {
		var tokenErr *liveTokenError
		if errors.As(err, &tokenErr) && tokenErr.Code == "invalid_grant" {
			f.status("cached Microsoft refresh token was rejected; browser approval required")
			_ = f.cache.clear()
			return nil, nil
		}
		return nil, err
	}
	if err := f.saveLiveToken(*token); err != nil {
		return nil, err
	}
	f.status("refreshed cached Microsoft token")
	return token, nil
}

func (f *flow) refreshLiveToken(ctx context.Context, refreshToken string) (*liveTokenResponse, error) {
	form := url.Values{}
	form.Set("scope", "service::user.auth.xboxlive.com::MBI_SSL")
	form.Set("client_id", f.clientID)
	form.Set("grant_type", "refresh_token")
	form.Set("refresh_token", refreshToken)

	var token liveTokenResponse
	status, err := f.doFormJSONAnyStatus(ctx, http.MethodPost, liveTokenURL, form, nil, &token)
	if err != nil {
		return nil, err
	}
	if status < 200 || status >= 300 {
		if token.Error != "" {
			return nil, &liveTokenError{Code: token.Error, Description: token.Description}
		}
		return nil, fmt.Errorf("refresh Microsoft token: unexpected status %d", status)
	}
	if token.AccessToken == "" {
		return nil, fmt.Errorf("refresh Microsoft token: missing access token")
	}
	return &token, nil
}

func (f *flow) getUserToken(ctx context.Context, msaAccessToken string) (string, error) {
	payload := map[string]any{
		"RelyingParty": "http://auth.xboxlive.com",
		"TokenType":    "JWT",
		"Properties": map[string]any{
			"AuthMethod": "RPS",
			"SiteName":   "user.auth.xboxlive.com",
			"RpsTicket":  "t=" + msaAccessToken,
		},
	}

	body, headers, err := f.signedJSONRequest(xboxUserAuthURL, payload)
	if err != nil {
		return "", err
	}
	headers.Set("Accept", "application/json")
	headers.Set("x-xbl-contract-version", "2")

	var resp xboxUserTokenResponse
	if err := f.doJSON(ctx, http.MethodPost, xboxUserAuthURL, body, headers, &resp); err != nil {
		return "", err
	}
	if resp.Token == "" {
		return "", fmt.Errorf("xbox user token response missing token")
	}
	return resp.Token, nil
}

func (f *flow) getDeviceToken(ctx context.Context) (string, error) {
	payload := map[string]any{
		"Properties": map[string]any{
			"AuthMethod":   "ProofOfPossession",
			"Id":           bracedUUID(),
			"DeviceType":   f.deviceType,
			"SerialNumber": bracedUUID(),
			"Version":      f.deviceVers,
			"ProofKey":     f.jwk,
		},
		"RelyingParty": "http://auth.xboxlive.com",
		"TokenType":    "JWT",
	}

	body, headers, err := f.signedJSONRequest(xboxDeviceAuthURL, payload)
	if err != nil {
		return "", err
	}

	var resp xboxUserTokenResponse
	if err := f.doJSON(ctx, http.MethodPost, xboxDeviceAuthURL, body, headers, &resp); err != nil {
		return "", err
	}
	if resp.Token == "" {
		return "", fmt.Errorf("xbox device token response missing token")
	}
	return resp.Token, nil
}

func (f *flow) getTitleToken(ctx context.Context, msaAccessToken, deviceToken string) (string, error) {
	payload := map[string]any{
		"Properties": map[string]any{
			"AuthMethod":  "RPS",
			"DeviceToken": deviceToken,
			"RpsTicket":   "t=" + msaAccessToken,
			"SiteName":    "user.auth.xboxlive.com",
			"ProofKey":    f.jwk,
		},
		"RelyingParty": "http://auth.xboxlive.com",
		"TokenType":    "JWT",
	}

	body, headers, err := f.signedJSONRequest(xboxTitleAuthURL, payload)
	if err != nil {
		return "", err
	}

	var resp xboxUserTokenResponse
	if err := f.doJSON(ctx, http.MethodPost, xboxTitleAuthURL, body, headers, &resp); err != nil {
		return "", err
	}
	if resp.Token == "" {
		return "", fmt.Errorf("xbox title token response missing token")
	}
	return resp.Token, nil
}

func (f *flow) getXSTSToken(ctx context.Context, userToken, deviceToken, titleToken string) (*xboxXSTS, error) {
	payload := map[string]any{
		"RelyingParty": "rp://api.minecraftservices.com/",
		"TokenType":    "JWT",
		"Properties": map[string]any{
			"UserTokens":  []string{userToken},
			"DeviceToken": deviceToken,
			"TitleToken":  titleToken,
			"ProofKey":    f.jwk,
			"SandboxId":   "RETAIL",
		},
	}

	body, headers, err := f.signedJSONRequest(xboxXSTSAuthorizeURL, payload)
	if err != nil {
		return nil, err
	}

	var resp xboxXSTSResponse
	if err := f.doJSON(ctx, http.MethodPost, xboxXSTSAuthorizeURL, body, headers, &resp); err != nil {
		return nil, err
	}
	if resp.XErr != 0 {
		if msg, ok := xboxLiveErrors[resp.XErr]; ok {
			return nil, fmt.Errorf("xsts auth failed: %s", msg)
		}
		return nil, fmt.Errorf("xsts auth failed: xerr=%d message=%s", resp.XErr, resp.Message)
	}
	if len(resp.DisplayClaims.XUI) == 0 || resp.Token == "" {
		return nil, fmt.Errorf("xsts response missing claims or token")
	}
	return &xboxXSTS{
		UserHash: resp.DisplayClaims.XUI[0].UHS,
		UserXUID: resp.DisplayClaims.XUI[0].XID,
		Token:    resp.Token,
	}, nil
}

func (f *flow) loginWithXbox(ctx context.Context, xsts *xboxXSTS) (string, error) {
	payload := map[string]string{
		"identityToken": fmt.Sprintf("XBL3.0 x=%s;%s", xsts.UserHash, xsts.Token),
	}

	body, err := json.Marshal(payload)
	if err != nil {
		return "", err
	}
	headers := makeJSONHeaders()

	var resp minecraftAuthResponse
	if err := f.doJSON(ctx, http.MethodPost, mcLoginWithXboxURL, body, headers, &resp); err != nil {
		return "", err
	}
	if resp.Error != "" {
		return "", fmt.Errorf("minecraft login failed: %s %s", resp.Error, resp.Path)
	}
	if resp.AccessToken == "" {
		return "", fmt.Errorf("minecraft login response missing access token")
	}
	return resp.AccessToken, nil
}

func (f *flow) fetchProfile(ctx context.Context, token string) (Profile, error) {
	headers := make(http.Header)
	headers.Set("Authorization", "Bearer "+token)
	headers.Set("User-Agent", defaultUserAgent)

	var profile Profile
	if err := f.doJSON(ctx, http.MethodGet, mcProfileURL, nil, headers, &profile); err != nil {
		return Profile{}, err
	}
	if profile.ID == "" || profile.Name == "" {
		return Profile{}, fmt.Errorf("minecraft profile response missing id or name")
	}
	return profile, nil
}

func (f *flow) status(message string) {
	if f.onStatus != nil {
		f.onStatus(message)
	}
}

func (f *flow) saveLiveToken(token liveTokenResponse) error {
	if f.cache == nil {
		return nil
	}
	state, err := f.cache.load()
	if err != nil {
		return err
	}
	if state == nil {
		state = &cachedLiveState{ClientID: f.clientID}
	}
	state.ClientID = f.clientID
	state.ObtainedAt = time.Now().UTC()
	state.Token = token
	return f.cache.save(*state)
}

func (f *flow) saveProfile(profile Profile) error {
	if f.cache == nil {
		return nil
	}
	state, err := f.cache.load()
	if err != nil {
		return err
	}
	if state == nil {
		return nil
	}
	state.Profile = &profile
	return f.cache.save(*state)
}

func (f *flow) signedJSONRequest(rawURL string, payload any) ([]byte, http.Header, error) {
	body, err := json.Marshal(payload)
	if err != nil {
		return nil, nil, err
	}
	signature, err := f.sign(rawURL, "", string(body))
	if err != nil {
		return nil, nil, err
	}
	headers := makeJSONHeaders()
	headers.Set("Cache-Control", "no-store, must-revalidate, no-cache")
	headers.Set("x-xbl-contract-version", "1")
	headers.Set("Signature", signature)
	return body, headers, nil
}

func (f *flow) sign(rawURL, authorizationToken, payload string) (string, error) {
	parsed, err := url.Parse(rawURL)
	if err != nil {
		return "", err
	}
	pathOnly := parsed.Path
	if pathOnly == "" {
		pathOnly = "/"
	}

	buf := bytes.NewBuffer(nil)
	_ = binary.Write(buf, binary.BigEndian, uint32(1))
	buf.WriteByte(0)
	windowsTS := uint64((time.Now().Unix() + 11644473600) * 10000000)
	_ = binary.Write(buf, binary.BigEndian, windowsTS)
	buf.WriteByte(0)
	writeNT(buf, "POST")
	writeNT(buf, pathOnly)
	writeNT(buf, authorizationToken)
	writeNT(buf, payload)

	hash := sha256.Sum256(buf.Bytes())
	r, s, err := ecdsa.Sign(rand.Reader, f.key, hash[:])
	if err != nil {
		return "", err
	}
	sig := make([]byte, 64)
	r.FillBytes(sig[:32])
	s.FillBytes(sig[32:])

	head := bytes.NewBuffer(nil)
	_ = binary.Write(head, binary.BigEndian, uint32(1))
	_ = binary.Write(head, binary.BigEndian, windowsTS)
	head.Write(sig)

	return base64.StdEncoding.EncodeToString(head.Bytes()), nil
}

func (f *flow) doFormJSON(ctx context.Context, method, rawURL string, form url.Values, headers http.Header, out any) error {
	_, err := f.doFormJSONStatus(ctx, method, rawURL, form, headers, out)
	return err
}

func (f *flow) doFormJSONAnyStatus(ctx context.Context, method, rawURL string, form url.Values, headers http.Header, out any) (int, error) {
	body := strings.NewReader(form.Encode())
	if headers == nil {
		headers = make(http.Header)
	}
	headers.Set("Content-Type", "application/x-www-form-urlencoded")
	return f.doRequestJSONAnyStatus(ctx, method, rawURL, body, headers, out)
}

func (f *flow) doFormJSONStatus(ctx context.Context, method, rawURL string, form url.Values, headers http.Header, out any) (int, error) {
	body := strings.NewReader(form.Encode())
	if headers == nil {
		headers = make(http.Header)
	}
	headers.Set("Content-Type", "application/x-www-form-urlencoded")
	return f.doRequestJSONStatus(ctx, method, rawURL, body, headers, out)
}

func (f *flow) doJSON(ctx context.Context, method, rawURL string, body []byte, headers http.Header, out any) error {
	_, err := f.doJSONStatus(ctx, method, rawURL, body, headers, out)
	return err
}

func (f *flow) doJSONStatus(ctx context.Context, method, rawURL string, body []byte, headers http.Header, out any) (int, error) {
	var reader io.Reader
	if body != nil {
		reader = bytes.NewReader(body)
	}
	return f.doRequestJSONStatus(ctx, method, rawURL, reader, headers, out)
}

func (f *flow) doRequestJSON(ctx context.Context, method, rawURL string, body io.Reader, headers http.Header, out any) error {
	_, err := f.doRequestJSONStatus(ctx, method, rawURL, body, headers, out)
	return err
}

func (f *flow) doRequestJSONAnyStatus(ctx context.Context, method, rawURL string, body io.Reader, headers http.Header, out any) (int, error) {
	req, err := http.NewRequestWithContext(ctx, method, rawURL, body)
	if err != nil {
		return 0, err
	}
	for key, values := range headers {
		for _, value := range values {
			req.Header.Add(key, value)
		}
	}
	resp, err := f.httpClient.Do(req)
	if err != nil {
		return 0, err
	}
	defer resp.Body.Close()

	data, err := io.ReadAll(resp.Body)
	if err != nil {
		return resp.StatusCode, err
	}
	if len(data) == 0 {
		data = []byte("{}")
	}
	if err := json.Unmarshal(data, out); err != nil {
		return resp.StatusCode, fmt.Errorf("decode json response: %w", err)
	}
	return resp.StatusCode, nil
}

func (f *flow) doRequestJSONStatus(ctx context.Context, method, rawURL string, body io.Reader, headers http.Header, out any) (int, error) {
	req, err := http.NewRequestWithContext(ctx, method, rawURL, body)
	if err != nil {
		return 0, err
	}
	for key, values := range headers {
		for _, value := range values {
			req.Header.Add(key, value)
		}
	}
	resp, err := f.httpClient.Do(req)
	if err != nil {
		return 0, err
	}
	defer resp.Body.Close()

	data, err := io.ReadAll(resp.Body)
	if err != nil {
		return resp.StatusCode, err
	}
	if len(data) == 0 {
		data = []byte("{}")
	}
	if err := json.Unmarshal(data, out); err != nil {
		return resp.StatusCode, fmt.Errorf("decode json response: %w", err)
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		var xstsErr xboxXSTSResponse
		if json.Unmarshal(data, &xstsErr) == nil && xstsErr.XErr != 0 {
			if msg, ok := xboxLiveErrors[xstsErr.XErr]; ok {
				return resp.StatusCode, fmt.Errorf("request failed: %s", msg)
			}
			return resp.StatusCode, fmt.Errorf("request failed: status=%s xerr=%d body=%s", resp.Status, xstsErr.XErr, string(data))
		}
		return resp.StatusCode, fmt.Errorf("request failed: status=%s body=%s", resp.Status, string(data))
	}
	return resp.StatusCode, nil
}

func publicJWK(key *ecdsa.PrivateKey) jwk {
	curve := key.Curve.Params()
	byteLen := (curve.BitSize + 7) / 8
	x := key.PublicKey.X.FillBytes(make([]byte, byteLen))
	y := key.PublicKey.Y.FillBytes(make([]byte, byteLen))
	return jwk{
		KTY: "EC",
		X:   base64.RawURLEncoding.EncodeToString(x),
		Y:   base64.RawURLEncoding.EncodeToString(y),
		CRV: "P-256",
		Alg: "ES256",
		Use: "sig",
	}
}

func bracedUUID() string {
	name := strconv.FormatInt(time.Now().UnixNano(), 10)
	id := uuid.NewMD5(uuid.NameSpaceDNS, []byte(name))
	return "{" + id.String() + "}"
}

func writeNT(buf *bytes.Buffer, value string) {
	buf.WriteString(value)
	buf.WriteByte(0)
}

func makeJSONHeaders() http.Header {
	headers := make(http.Header)
	headers.Set("Content-Type", "application/json")
	headers.Set("User-Agent", defaultUserAgent)
	return headers
}
