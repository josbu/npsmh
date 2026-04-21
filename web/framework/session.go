package framework

import (
	"crypto/rand"
	"crypto/sha256"
	"fmt"
	"net/http"
	"sync"

	"github.com/djylb/nps/lib/logs"
	"github.com/djylb/nps/lib/servercfg"
	"github.com/gin-gonic/gin"
	"github.com/gorilla/sessions"
)

const (
	sessionName       = "nps_session"
	sessionContextKey = "nps.session"
	requestDataKey    = "nps.request_data"
	requestParamsKey  = "nps.request_params"
	requestRawBodyKey = "nps.request_raw_body"
)

type sessionState struct {
	store   *sessions.CookieStore
	session *sessions.Session
	writer  http.ResponseWriter
	request *http.Request
}

type SessionEditor interface {
	Set(string, interface{})
	Delete(string)
}

type sessionBatchEditor struct {
	state *sessionState
}

var (
	sessionMu             sync.Mutex
	sessionStore          *sessions.CookieStore
	sessionConfigProvider = servercfg.Current
)

func InitSessionStore(cfg *servercfg.Snapshot) error {
	sessionMu.Lock()
	defer sessionMu.Unlock()
	if cfg == nil {
		cfg = currentSessionConfigLocked()
	}
	store, err := buildSessionStore(cfg)
	if err != nil {
		return err
	}
	sessionStore = store
	return nil
}

func SetSessionConfigProvider(provider func() *servercfg.Snapshot) {
	sessionMu.Lock()
	defer sessionMu.Unlock()
	if provider == nil {
		sessionConfigProvider = servercfg.Current
		return
	}
	sessionConfigProvider = provider
}

func newSessionState(w http.ResponseWriter, r *http.Request, cfg *servercfg.Snapshot) (*sessionState, error) {
	sessionMu.Lock()
	if sessionStore == nil {
		store, err := buildSessionStore(cfg)
		if err != nil {
			sessionMu.Unlock()
			return nil, err
		}
		sessionStore = store
	}
	store := sessionStore
	sessionMu.Unlock()

	session, err := store.Get(r, sessionName)
	if err != nil {
		logs.Warn("Invalid web session cookie, resetting session: %v", err)
		_ = clearSessionCookie(store, w, r)
		session = newBlankSession(store)
	}
	return &sessionState{
		store:   store,
		session: session,
		writer:  w,
		request: r,
	}, nil
}

func EnsureSession(g *gin.Context) error {
	if sessionStateFromContext(g) != nil {
		return nil
	}
	session, err := newSessionState(g.Writer, g.Request, currentSessionConfig())
	if err != nil {
		return err
	}
	g.Set(sessionContextKey, session)
	return nil
}

func SessionValue(g *gin.Context, key string) interface{} {
	session := sessionStateFromContext(g)
	if session == nil {
		return nil
	}
	return session.Get(key)
}

func SetSessionValue(g *gin.Context, key string, value interface{}) error {
	if err := EnsureSession(g); err != nil {
		return err
	}
	session := sessionStateFromContext(g)
	if session == nil {
		return nil
	}
	return session.Set(key, value)
}

func MutateSession(g *gin.Context, fn func(SessionEditor)) error {
	if err := EnsureSession(g); err != nil {
		return err
	}
	session := sessionStateFromContext(g)
	if session == nil || fn == nil {
		return nil
	}
	fn(sessionBatchEditor{state: session})
	return session.save()
}

func DeleteSessionValue(g *gin.Context, key string) error {
	if err := EnsureSession(g); err != nil {
		return err
	}
	session := sessionStateFromContext(g)
	if session == nil {
		return nil
	}
	return session.Delete(key)
}

func sessionStateFromContext(g *gin.Context) *sessionState {
	if v, ok := g.Get(sessionContextKey); ok {
		if session, ok := v.(*sessionState); ok {
			return session
		}
	}
	return nil
}

func SetRequestData(g *gin.Context, key string, value interface{}) {
	data := requestDataFromContext(g)
	if data == nil {
		data = make(map[string]interface{})
		g.Set(requestDataKey, data)
	}
	data[key] = value
	g.Set(key, value)
}

func SetRequestParam(g *gin.Context, key, value string) {
	params := requestParamsFromContext(g)
	if params == nil {
		params = make(map[string]string)
		g.Set(requestParamsKey, params)
	}
	params[key] = value
}

func RequestParam(g *gin.Context, key string) (string, bool) {
	params := requestParamsFromContext(g)
	if params == nil {
		return "", false
	}
	value, ok := params[key]
	return value, ok
}

func SetRequestRawBody(g *gin.Context, body []byte) {
	if g == nil {
		return
	}
	if body == nil {
		g.Set(requestRawBodyKey, []byte(nil))
		return
	}
	g.Set(requestRawBodyKey, append([]byte(nil), body...))
}

// RequestRawBodyView exposes the captured request body without an extra read-side
// clone. Callers must treat the returned bytes as read-only.
func RequestRawBodyView(g *gin.Context) []byte {
	if g == nil {
		return nil
	}
	if v, ok := g.Get(requestRawBodyKey); ok {
		if body, ok := v.([]byte); ok {
			return body
		}
	}
	return nil
}

func RequestRawBody(g *gin.Context) []byte {
	body := RequestRawBodyView(g)
	if len(body) == 0 {
		return body
	}
	return append([]byte(nil), body...)
}

func requestDataFromContext(g *gin.Context) map[string]interface{} {
	if v, ok := g.Get(requestDataKey); ok {
		if data, ok := v.(map[string]interface{}); ok {
			return data
		}
	}
	return nil
}

func requestParamsFromContext(g *gin.Context) map[string]string {
	if v, ok := g.Get(requestParamsKey); ok {
		if params, ok := v.(map[string]string); ok {
			return params
		}
	}
	return nil
}

func (s *sessionState) Get(key string) interface{} {
	if s == nil || s.session == nil {
		return nil
	}
	return s.session.Values[key]
}

func (s *sessionState) Set(key string, value interface{}) error {
	if s == nil || s.session == nil {
		return nil
	}
	s.session.Values[key] = value
	return s.session.Save(s.request, s.writer)
}

func (s *sessionState) Delete(key string) error {
	if s == nil || s.session == nil {
		return nil
	}
	delete(s.session.Values, key)
	return s.save()
}

func (s *sessionState) save() error {
	if s == nil || s.session == nil {
		return nil
	}
	return s.session.Save(s.request, s.writer)
}

func (e sessionBatchEditor) Set(key string, value interface{}) {
	if e.state == nil || e.state.session == nil {
		return
	}
	e.state.session.Values[key] = value
}

func (e sessionBatchEditor) Delete(key string) {
	if e.state == nil || e.state.session == nil {
		return
	}
	delete(e.state.session.Values, key)
}

func buildSessionStore(cfg *servercfg.Snapshot) (*sessions.CookieStore, error) {
	if cfg == nil {
		cfg = currentSessionConfigLocked()
	}
	authKey, encKey, err := deriveSessionKeys(cfg)
	if err != nil {
		return nil, err
	}
	store := sessions.NewCookieStore(authKey, encKey)
	cookiePath := "/"
	if cfg != nil {
		basePath := servercfg.NormalizeBaseURL(cfg.Web.BaseURL)
		if basePath != "" && basePath != "/" {
			cookiePath = basePath
		}
	}
	store.Options = &sessions.Options{
		Path:     cookiePath,
		HttpOnly: true,
		Secure:   cfg != nil && cfg.Security.SecureMode,
		SameSite: http.SameSiteLaxMode,
	}
	return store, nil
}

func clearSessionCookie(store *sessions.CookieStore, w http.ResponseWriter, r *http.Request) error {
	session := newBlankSession(store)
	session.Options.MaxAge = -1
	if err := session.Save(r, w); err != nil {
		return err
	}
	if session.Options == nil || session.Options.Path == "/" {
		return nil
	}
	rootSession := newBlankSession(store)
	rootSession.Options.MaxAge = -1
	rootSession.Options.Path = "/"
	return rootSession.Save(r, w)
}

func newBlankSession(store *sessions.CookieStore) *sessions.Session {
	session := sessions.NewSession(store, sessionName)
	if store != nil && store.Options != nil {
		options := *store.Options
		session.Options = &options
	}
	session.IsNew = true
	return session
}

func deriveSessionKeys(cfg *servercfg.Snapshot) ([]byte, []byte, error) {
	seed := fmt.Sprintf("%s|%s|%s|%s", cfg.Web.Username, cfg.Web.Password, cfg.Auth.Key, cfg.App.Name)
	if seed == "|||" {
		authKey := make([]byte, 32)
		encKey := make([]byte, 32)
		if _, err := rand.Read(authKey); err != nil {
			return nil, nil, err
		}
		if _, err := rand.Read(encKey); err != nil {
			return nil, nil, err
		}
		return authKey, encKey, nil
	}
	authSum := sha256.Sum256([]byte("auth:" + seed))
	encSum := sha256.Sum256([]byte("enc:" + seed))
	return authSum[:], encSum[:], nil
}

func currentSessionConfig() *servercfg.Snapshot {
	sessionMu.Lock()
	defer sessionMu.Unlock()
	return currentSessionConfigLocked()
}

func currentSessionConfigLocked() *servercfg.Snapshot {
	return servercfg.ResolveProvider(sessionConfigProvider)
}
