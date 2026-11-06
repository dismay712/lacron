package lacron

import (
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"slices"
	"strings"
	"sync"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/go-webauthn/webauthn/protocol"
	"github.com/go-webauthn/webauthn/webauthn"
)

const (
	sessionCookie = "lacron_session"
	sessionTTL    = 12 * time.Hour
	challengeTTL  = 2 * time.Minute
	maxPasskeys   = 16
)

type storedPasskey struct {
	Name       string              `json:"name"`
	CreatedAt  time.Time           `json:"created_at"`
	LastUsedAt *time.Time          `json:"last_used_at,omitempty"`
	Credential webauthn.Credential `json:"credential"`
}

func (p storedPasskey) id() string { return base64.RawURLEncoding.EncodeToString(p.Credential.ID) }

type passkeyView struct {
	ID         string     `json:"id"`
	Name       string     `json:"name"`
	CreatedAt  time.Time  `json:"created_at"`
	LastUsedAt *time.Time `json:"last_used_at,omitempty"`
}

func (p storedPasskey) view() passkeyView {
	return passkeyView{p.id(), p.Name, p.CreatedAt, p.LastUsedAt}
}

type passkeyUser struct {
	id          string
	credentials []webauthn.Credential
}

func (u passkeyUser) WebAuthnID() []byte                         { return []byte(u.id) }
func (u passkeyUser) WebAuthnName() string                       { return "admin" }
func (u passkeyUser) WebAuthnDisplayName() string                { return "lacron 管理员" }
func (u passkeyUser) WebAuthnCredentials() []webauthn.Credential { return u.credentials }

// The caller holds either s.mu or s.writeMu.
func (s *Service) passkeyAccount() passkeyUser {
	user := passkeyUser{id: s.passkeyUser, credentials: make([]webauthn.Credential, len(s.passkeys))}
	for i, key := range s.passkeys {
		user.credentials[i] = key.Credential
	}
	return user
}

func (s *Service) savePasskeys(keys []storedPasskey) error {
	if err := writeRow(s.db, saveMetaRow, "passkeys", keys); err != nil {
		return err
	}
	s.mu.Lock()
	s.passkeys = keys
	s.changes.Settings++
	s.mu.Unlock()
	return nil
}

type browserSession struct {
	expires time.Time
	passkey string
	policy  uint64
}

type ceremony struct {
	expires time.Time
	owner   string // Empty only for login. Registration is bound to its initiating session.
	name    string
	policy  uint64
	data    webauthn.SessionData
}

type auth struct {
	service    *Service
	token      [32]byte
	mu         sync.Mutex
	sessions   map[[32]byte]browserSession
	ceremonies map[string]ceremony
}

func newAuth(s *Service, token string) *auth {
	return &auth{s, sha256.Sum256([]byte(token)), sync.Mutex{}, map[[32]byte]browserSession{}, map[string]ceremony{}}
}

func (a *auth) bearer(r *http.Request) bool {
	token, ok := strings.CutPrefix(r.Header.Get("Authorization"), "Bearer ")
	digest := sha256.Sum256([]byte(token))
	return ok && subtle.ConstantTimeCompare(digest[:], a.token[:]) == 1
}

func (a *auth) session(r *http.Request) string {
	c, err := r.Cookie(sessionCookie)
	if err != nil || !idPattern.MatchString(c.Value) {
		return ""
	}
	key := sha256.Sum256([]byte(c.Value))
	a.mu.Lock()
	session, ok := a.sessions[key]
	if ok && !time.Now().Before(session.expires) {
		delete(a.sessions, key)
		ok = false
	}
	a.mu.Unlock()
	if !ok {
		return ""
	}
	if session.passkey != "" {
		s := a.service
		s.mu.RLock()
		valid := s.settings.PasskeyEnabled && session.policy == s.passkeyPolicy && slices.ContainsFunc(s.passkeys, func(p storedPasskey) bool { return p.id() == session.passkey })
		s.mu.RUnlock()
		if !valid {
			a.mu.Lock()
			delete(a.sessions, key)
			a.mu.Unlock()
			return ""
		}
	}
	return c.Value
}

func (a *auth) identity(r *http.Request) string {
	if a.bearer(r) {
		return "bearer"
	}
	return a.session(r)
}

func requestOrigin(r *http.Request) *url.URL {
	u, err := url.Parse(r.Header.Get("Origin"))
	if err != nil || u.Host == "" || (u.Scheme != "https" && u.Scheme != "http") || u.User != nil || u.Path != "" || u.RawQuery != "" || u.ForceQuery || u.Fragment != "" {
		return nil
	}
	// A default port may be present in Host even if omitted by the browser.
	normalize := func(host string) string {
		_, port, err := net.SplitHostPort(host)
		if err == nil && (u.Scheme == "https" && port == "443" || u.Scheme == "http" && port == "80") {
			return strings.TrimSuffix(host, ":"+port)
		}
		return host
	}
	if !strings.EqualFold(normalize(u.Host), normalize(r.Host)) {
		return nil
	}
	return u
}

func (a *auth) protect(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		identity := a.identity(r)
		if identity == "" {
			w.Header().Set("WWW-Authenticate", "Bearer")
			problem(w, http.StatusUnauthorized, "请先登录或检查访问令牌")
			return
		}
		if r.Method != http.MethodGet && r.Method != http.MethodHead && (identity != "bearer" || r.Header.Get("Origin") != "") && requestOrigin(r) == nil {
			problem(w, http.StatusForbidden, "拒绝跨站请求")
			return
		}
		next.ServeHTTP(w, r)
	})
}

func setSessionCookie(w http.ResponseWriter, r *http.Request, id string, maxAge int) {
	origin := requestOrigin(r)
	http.SetCookie(w, &http.Cookie{
		Name: sessionCookie, Value: id, Path: "/", MaxAge: maxAge,
		HttpOnly: true, SameSite: http.SameSiteStrictMode, Secure: r.TLS != nil || origin != nil && origin.Scheme == "https",
	})
}

func (a *auth) issueSession(w http.ResponseWriter, r *http.Request, passkey string, policy uint64) {
	now := time.Now()
	a.mu.Lock()
	for id, session := range a.sessions {
		if !now.Before(session.expires) {
			delete(a.sessions, id)
		}
	}
	if len(a.sessions) >= 128 {
		a.mu.Unlock()
		problem(w, http.StatusTooManyRequests, "浏览器会话已满，请退出旧会话后重试")
		return
	}
	if old, err := r.Cookie(sessionCookie); err == nil {
		delete(a.sessions, sha256.Sum256([]byte(old.Value)))
	}
	id := newID()
	a.sessions[sha256.Sum256([]byte(id))] = browserSession{now.Add(sessionTTL), passkey, policy}
	a.mu.Unlock()
	setSessionCookie(w, r, id, int(sessionTTL.Seconds()))
	w.WriteHeader(http.StatusNoContent)
}

func (a *auth) tokenLogin(w http.ResponseWriter, r *http.Request) {
	if requestOrigin(r) == nil {
		problem(w, http.StatusForbidden, "拒绝跨站请求")
		return
	}
	if !a.bearer(r) {
		problem(w, http.StatusUnauthorized, "访问令牌无效")
		return
	}
	a.issueSession(w, r, "", 0)
}

func (a *auth) logout(w http.ResponseWriter, r *http.Request) {
	if requestOrigin(r) == nil {
		problem(w, http.StatusForbidden, "拒绝跨站请求")
		return
	}
	if c, err := r.Cookie(sessionCookie); err == nil {
		a.mu.Lock()
		delete(a.sessions, sha256.Sum256([]byte(c.Value)))
		a.mu.Unlock()
	}
	setSessionCookie(w, r, "", -1)
	w.WriteHeader(http.StatusNoContent)
}

func (a *auth) status(w http.ResponseWriter, r *http.Request) {
	s := a.service
	s.mu.RLock()
	ready, origin := s.settings.PasskeyEnabled && len(s.passkeys) > 0, s.settings.PasskeyOrigin
	s.mu.RUnlock()
	respond(w, http.StatusOK, map[string]any{"passkey_ready": ready, "passkey_origin": origin})
}

func passkeyServer(settings Settings, r *http.Request) (*webauthn.WebAuthn, error) {
	origin, rpID, err := parsePasskeyOrigin(settings.PasskeyOrigin)
	provided := requestOrigin(r)
	if !settings.PasskeyEnabled || err != nil || provided == nil || provided.String() != origin {
		return nil, fmt.Errorf("请在系统设置中启用 Passkey，并从配置的站点地址访问")
	}
	return webauthn.New(&webauthn.Config{RPID: rpID, RPDisplayName: "lacron", RPOrigins: []string{origin}})
}

func (a *auth) begin(w http.ResponseWriter, r *http.Request, register bool) {
	owner, name := "", ""
	if register {
		owner = a.identity(r)
		if owner == "" {
			problem(w, http.StatusUnauthorized, "请先登录")
			return
		}
		var in struct {
			Name string `json:"name"`
		}
		if decodeJSON(w, r, &in, 4096) != nil {
			problem(w, http.StatusBadRequest, "设备名称无效")
			return
		}
		name = strings.TrimSpace(in.Name)
		if name == "" || utf8.RuneCountInString(name) > 80 || strings.ContainsFunc(name, unicode.IsControl) {
			problem(w, http.StatusBadRequest, "设备名称须为 1–80 字符")
			return
		}
	}
	s := a.service
	s.mu.RLock()
	settings, policy, user := s.settings, s.passkeyPolicy, s.passkeyAccount()
	s.mu.RUnlock()
	server, err := passkeyServer(settings, r)
	if err != nil {
		problem(w, http.StatusForbidden, err.Error())
		return
	}
	var options any
	var data *webauthn.SessionData
	if register {
		if len(user.credentials) >= maxPasskeys {
			problem(w, http.StatusConflict, "已达到 Passkey 数量上限")
			return
		}
		exclude := make([]protocol.CredentialDescriptor, 0, len(user.credentials))
		for _, key := range user.credentials {
			exclude = append(exclude, key.Descriptor())
		}
		options, data, err = server.BeginRegistration(user, webauthn.WithExclusions(exclude),
			webauthn.WithAuthenticatorSelection(protocol.AuthenticatorSelection{
				ResidentKey: protocol.ResidentKeyRequirementPreferred, UserVerification: protocol.VerificationRequired,
			}))
	} else {
		if len(user.credentials) == 0 {
			problem(w, http.StatusForbidden, "尚未绑定 Passkey")
			return
		}
		options, data, err = server.BeginLogin(user, webauthn.WithUserVerification(protocol.VerificationRequired))
	}
	if err != nil {
		apiError(w, err)
		return
	}
	now := time.Now()
	a.mu.Lock()
	for id, pending := range a.ceremonies {
		if !now.Before(pending.expires) {
			delete(a.ceremonies, id)
		}
	}
	if len(a.ceremonies) >= 128 {
		a.mu.Unlock()
		problem(w, http.StatusTooManyRequests, "Passkey 请求过多，请稍后重试")
		return
	}
	id := newID()
	a.ceremonies[id] = ceremony{now.Add(challengeTTL), owner, name, policy, *data}
	a.mu.Unlock()
	respond(w, http.StatusOK, map[string]any{"challenge_id": id, "options": options})
}

func (a *auth) finish(w http.ResponseWriter, r *http.Request, register bool) {
	var in struct {
		ID         string          `json:"challenge_id"`
		Credential json.RawMessage `json:"credential"`
	}
	if decodeJSON(w, r, &in, 128<<10) != nil || !idPattern.MatchString(in.ID) {
		problem(w, http.StatusBadRequest, "Passkey 响应格式无效")
		return
	}
	owner := ""
	if register {
		owner = a.identity(r)
		if owner == "" {
			problem(w, http.StatusUnauthorized, "请先登录")
			return
		}
	}
	s := a.service
	s.writeMu.Lock()
	defer s.writeMu.Unlock()
	if s.closing {
		apiError(w, ErrClosing)
		return
	}
	server, err := passkeyServer(s.settings, r)
	if err != nil {
		problem(w, http.StatusForbidden, err.Error())
		return
	}
	a.mu.Lock()
	pending, ok := a.ceremonies[in.ID]
	if ok && pending.owner == owner {
		delete(a.ceremonies, in.ID)
	}
	a.mu.Unlock()
	if !ok || pending.owner != owner || pending.policy != s.passkeyPolicy || !time.Now().Before(pending.expires) {
		problem(w, http.StatusBadRequest, "Passkey 请求已失效，请重试")
		return
	}
	user := s.passkeyAccount()
	keys := slices.Clone(s.passkeys)
	if register {
		a.register(w, server, user, keys, pending, in.Credential)
		return
	}
	parsed, err := protocol.ParseCredentialRequestResponseBytes(in.Credential)
	if err == nil {
		var credential *webauthn.Credential
		credential, err = server.ValidateLogin(user, pending.data, parsed)
		if err == nil && !credential.Authenticator.CloneWarning {
			id := base64.RawURLEncoding.EncodeToString(credential.ID)
			if i := slices.IndexFunc(keys, func(key storedPasskey) bool { return key.id() == id }); i >= 0 {
				now := time.Now().UTC()
				keys[i].Credential, keys[i].LastUsedAt = *credential, &now
				if err := s.savePasskeys(keys); err != nil {
					apiError(w, err)
					return
				}
				a.issueSession(w, r, id, pending.policy)
				return
			}
		}
	}
	problem(w, http.StatusUnauthorized, "Passkey 验证失败")
}

func (a *auth) register(w http.ResponseWriter, server *webauthn.WebAuthn, user passkeyUser, keys []storedPasskey, pending ceremony, response []byte) {
	parsed, err := protocol.ParseCredentialCreationResponseBytes(response)
	if err != nil {
		problem(w, http.StatusBadRequest, "Passkey 注册响应无效")
		return
	}
	credential, err := server.CreateCredential(user, pending.data, parsed)
	if err != nil {
		problem(w, http.StatusBadRequest, "Passkey 注册验证失败")
		return
	}
	key := storedPasskey{Name: pending.name, CreatedAt: time.Now().UTC(), Credential: *credential}
	if len(keys) >= maxPasskeys || slices.ContainsFunc(keys, func(existing storedPasskey) bool { return existing.id() == key.id() }) {
		problem(w, http.StatusConflict, "Passkey 已绑定，或已达到数量上限")
		return
	}
	if err := a.service.savePasskeys(append(keys, key)); err != nil {
		apiError(w, err)
		return
	}
	respond(w, http.StatusCreated, key.view())
}

func (a *auth) list(w http.ResponseWriter, r *http.Request) {
	s := a.service
	s.mu.RLock()
	keys := make([]passkeyView, len(s.passkeys))
	for i, key := range s.passkeys {
		keys[i] = key.view()
	}
	s.mu.RUnlock()
	respond(w, http.StatusOK, keys)
}

func (a *auth) remove(w http.ResponseWriter, r *http.Request) {
	s := a.service
	s.writeMu.Lock()
	defer s.writeMu.Unlock()
	if s.closing {
		apiError(w, ErrClosing)
		return
	}
	id := r.PathValue("id")
	i := slices.IndexFunc(s.passkeys, func(key storedPasskey) bool { return key.id() == id })
	if i < 0 {
		problem(w, http.StatusNotFound, "Passkey 不存在")
		return
	}
	keys := slices.Delete(slices.Clone(s.passkeys), i, i+1)
	if err := s.savePasskeys(keys); err != nil {
		apiError(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
