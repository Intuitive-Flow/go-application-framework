package auth

import (
	"context"
	crypto_rand "crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"html"
	"math/rand"
	"net"
	"net/http"
	"sync"
	"time"

	"github.com/pkg/browser"
	"golang.org/x/oauth2"

	"github.com/snyk/go-application-framework/pkg/configuration"
)

const (
	CONFIG_KEY_OAUTH_TOKEN string        = "INTERNAL_OAUTH_TOKEN_STORAGE"
	OAUTH_CLIENT_ID        string        = "b56d4c2e-b9e1-4d27-8773-ad47eafb0956"
	CALLBACK_HOSTNAME      string        = "127.0.0.1"
	CALLBACK_PATH          string        = "/authorization-code/callback"
	TIMEOUT_SECONDS        time.Duration = 120 * time.Second
	AUTHENTICATED_MESSAGE                = "Your account has been authenticated."
)

var _ Authenticator = (*oAuth2Authenticator)(nil)

var acceptedCallbackPorts = []int{8080, 18081, 28082, 38083, 48084}
var globalRefreshMutex sync.Mutex

type oAuth2Authenticator struct {
	httpClient         *http.Client
	config             configuration.Configuration
	oauthConfig        *oauth2.Config
	token              *oauth2.Token
	headless           bool
	openBrowserFunc    func(authUrl string)
	shutdownServerFunc func(server *http.Server)
	tokenRefresherFunc func(ctx context.Context, oauthConfig *oauth2.Config, token *oauth2.Token) (*oauth2.Token, error)
}

func init() {
	var seed int64
	var b [8]byte

	// try to use a real random value to seed the pseudo random number generator and
	// only fall back to time based seed if this didn't work.
	_, err := crypto_rand.Read(b[:])
	if err != nil {
		seed = time.Now().UnixNano() // fallback to time only if necessary
	} else {
		seed = int64(binary.LittleEndian.Uint64(b[:])) // based on https://stackoverflow.com/a/54491783
	}
	rand.Seed(seed)
}

func OpenBrowser(authUrl string) {
	_ = browser.OpenURL(authUrl)
}

func ShutdownServer(server *http.Server) {
	time.Sleep(500)
	_ = server.Shutdown(context.Background())
}

func getRedirectUri(port int) string {
	callback := fmt.Sprintf("http://%s:%d%s", CALLBACK_HOSTNAME, port, CALLBACK_PATH)
	return callback
}

func getOAuthConfiguration(config configuration.Configuration) *oauth2.Config {
	appUrl := config.GetString(configuration.WEB_APP_URL)
	apiUrl := config.GetString(configuration.API_URL)
	tokenUrl := apiUrl + "/oauth2/token"
	authUrl := appUrl + "/oauth2/authorize"

	conf := &oauth2.Config{
		ClientID: OAUTH_CLIENT_ID,
		Endpoint: oauth2.Endpoint{
			TokenURL: tokenUrl,
			AuthURL:  authUrl,
		},
	}
	return conf
}

// This method creates a code challenge as defined in https://www.rfc-editor.org/rfc/rfc7636#section-4.2
// It accepts a code verifier and returns the challenge as a URL safe string.
func getCodeChallenge(verifier []byte) string {
	shasum := sha256.Sum256(verifier)
	return base64.RawURLEncoding.EncodeToString(shasum[:])
}

// This method creates a code verifier as defined in https://www.rfc-editor.org/rfc/rfc7636#section-4.1
// It accepts the number of bytes it shall create and returns the verifier as a byte slice of the specified length.
func createVerifier(count int) []byte {
	/*
	  unreserved = ALPHA / DIGIT / "-" / "." / "_" / "~"
	   ALPHA = %x41-5A / %x61-7A
	   DIGIT = %x30-39
	*/
	lut := []byte("ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz0123456789-._~")
	verifier := make([]byte, count)

	// TODO is this good enough?
	for i := range verifier {
		index := rand.Int() % len(lut)
		verifier[i] = lut[index]
	}

	return verifier
}

// GetOAuthToken extracts an oauth2.Token from the given configuration instance if available
func GetOAuthToken(config configuration.Configuration) (*oauth2.Token, error) {
	oauthTokenString := config.GetString(CONFIG_KEY_OAUTH_TOKEN)
	if len(oauthTokenString) > 0 {
		token := &oauth2.Token{}
		err := json.Unmarshal([]byte(oauthTokenString), token)
		if err != nil {
			return nil, err
		}
		return token, nil
	}
	return nil, nil
}

func RefreshToken(ctx context.Context, oauthConfig *oauth2.Config, token *oauth2.Token) (*oauth2.Token, error) {
	tokenSource := oauthConfig.TokenSource(ctx, token)
	return tokenSource.Token()
}

//goland:noinspection GoUnusedExportedFunction
func NewOAuth2Authenticator(config configuration.Configuration, httpClient *http.Client) Authenticator {
	return NewOAuth2AuthenticatorWithOpts(config, WithHttpClient(httpClient))
}

func NewOAuth2AuthenticatorWithOpts(config configuration.Configuration, opts ...OAuth2AuthenticatorOption) Authenticator {
	o := &oAuth2Authenticator{}
	o.config = config
	o.token, _ = GetOAuthToken(config)
	o.oauthConfig = getOAuthConfiguration(config)
	config.PersistInStorage(CONFIG_KEY_OAUTH_TOKEN)

	// set defaults
	o.httpClient = http.DefaultClient
	o.openBrowserFunc = OpenBrowser
	o.shutdownServerFunc = ShutdownServer
	o.tokenRefresherFunc = RefreshToken

	// apply options
	for _, opt := range opts {
		opt(o)
	}
	return o
}

// Deprecated: use NewOAuth2AuthenticatorWithOpts instead
//
//goland:noinspection GoUnusedExportedFunction
func NewOAuth2AuthenticatorWithCustomFuncs(
	config configuration.Configuration,
	httpClient *http.Client,
	openBrowserFunc func(url string),
	shutdownServerFunc func(server *http.Server),
) Authenticator {
	return NewOAuth2AuthenticatorWithOpts(
		config,
		WithHttpClient(httpClient),
		WithOpenBrowserFunc(openBrowserFunc),
		WithShutdownServerFunc(shutdownServerFunc),
	)
}

func (o *oAuth2Authenticator) IsSupported() bool {
	tokenExistent := o.token != nil
	featureEnabled := o.config.GetBool(configuration.FF_OAUTH_AUTH_FLOW_ENABLED)
	return tokenExistent && featureEnabled
}

func (o *oAuth2Authenticator) persistToken(token *oauth2.Token) {
	tokenstring, _ := json.Marshal(token)
	o.config.Set(CONFIG_KEY_OAUTH_TOKEN, string(tokenstring))
	o.token = token
}

func (o *oAuth2Authenticator) Authenticate() error {
	var responseCode string
	var responseState string
	var responseError string
	verifier := createVerifier(128)
	state := string(createVerifier(15))
	codeChallenge := getCodeChallenge(verifier)
	ctx := context.Background()

	if o.headless {
		return errors.New("headless mode not supported")
	}

	mux := http.NewServeMux()

	srv := &http.Server{
		Handler: mux,
	}
	mux.HandleFunc(CALLBACK_PATH, func(w http.ResponseWriter, r *http.Request) {
		responseError = html.EscapeString(r.URL.Query().Get("error"))
		if len(responseError) > 0 {
			details := html.EscapeString(r.URL.Query().Get("error_description"))
			_, _ = fmt.Fprintf(w, "Failed to authenticate. (%s)\n%s", responseError, details)
		} else {
			responseCode = html.EscapeString(r.URL.Query().Get("code"))
			responseState = html.EscapeString(r.URL.Query().Get("state"))
			_, _ = fmt.Fprint(w, AUTHENTICATED_MESSAGE)
		}

		go o.shutdownServerFunc(srv)
	})

	// iterate over different known ports if one fails
	for _, port := range acceptedCallbackPorts {
		srv.Addr = fmt.Sprintf("%s:%d", CALLBACK_HOSTNAME, port)
		listener, err := net.Listen("tcp", srv.Addr)
		if err != nil { // skip port if it can't be listened to
			continue
		}

		// fill redirect url now that the port is known
		o.oauthConfig.RedirectURL = getRedirectUri(port)

		url := o.oauthConfig.AuthCodeURL(state, oauth2.AccessTypeOffline,
			oauth2.SetAuthURLParam("code_challenge", codeChallenge),
			oauth2.SetAuthURLParam("code_challenge_method", "S256"),
			oauth2.SetAuthURLParam("response_type", "code"),
			oauth2.SetAuthURLParam("scope", "offline_access"),
			oauth2.SetAuthURLParam("version", "2021-08-11~experimental"))

		// launch browser
		go o.openBrowserFunc(url)

		timedOut := false
		timer := time.AfterFunc(TIMEOUT_SECONDS, func() {
			timedOut = true
			o.shutdownServerFunc(srv)
		})
		err = srv.Serve(listener)
		if err == http.ErrServerClosed { // if the server was shutdown normally, there is no need to iterate further
			if timedOut {
				return errors.New("authentication failed (timeout)")
			}
			timer.Stop()
			break
		}
	}

	if len(responseError) > 0 {
		return fmt.Errorf("authentication error: %s", responseError)
	}

	// check the response state before continuing
	if state != responseState {
		return fmt.Errorf("incorrect response state: %s != %s", responseState, state)
	}

	// Use the custom HTTP client when requesting a token.
	if o.httpClient != nil {
		ctx = context.WithValue(ctx, oauth2.HTTPClient, o.httpClient)
	}

	token, err := o.oauthConfig.Exchange(ctx, responseCode, oauth2.SetAuthURLParam("code_verifier", string(verifier)))
	if err != nil {
		return err
	}

	o.persistToken(token)
	return nil
}

func (o *oAuth2Authenticator) AddAuthenticationHeader(request *http.Request) error {
	if request == nil {
		return fmt.Errorf("request must not be nil")
	}
	if o.token == nil {
		return fmt.Errorf("oauth token must not be nil to authorize")
	}

	ctx := context.Background()

	if o.httpClient != nil {
		ctx = context.WithValue(ctx, oauth2.HTTPClient, o.httpClient)
	}

	// if the current token is invalid
	if !o.token.Valid() {
		globalRefreshMutex.Lock()
		defer globalRefreshMutex.Unlock()

		// check if the token in the config is invalid as well
		token, _ := GetOAuthToken(o.config)
		if !token.Valid() {
			// use TokenSource to refresh the token
			validToken, err := o.tokenRefresherFunc(ctx, o.oauthConfig, o.token)
			if err != nil {
				return err
			}

			if validToken != o.token {
				o.persistToken(validToken)
			}
		} else {
			o.token = token
		}
	}

	accessToken := o.token.AccessToken
	if len(accessToken) > 0 {
		value := fmt.Sprint("Bearer ", accessToken)
		request.Header.Set("Authorization", value)
		request.Header.Set("Session-Token", value)
	}

	return nil
}
