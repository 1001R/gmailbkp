package main

import (
	_ "embed"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"regexp"
	"strings"

	"github.com/google/uuid"
)

//go:embed client_id
var CLIENT_ID string

//go:embed client_secret
var CLIENT_SECRET string

type Authenticator struct {
	state    string
	listener net.Listener
}

type TokenResponse struct {
	AccessToken string `json:"access_token"`
}

func NewAuthenticator() (*Authenticator, error) {
	listener, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		return nil, err
	}
	auth := &Authenticator{
		state:    uuid.Must(uuid.NewRandom()).String(),
		listener: listener,
	}
	return auth, nil
}

func (auth *Authenticator) Port() int {
	return auth.listener.Addr().(*net.TCPAddr).Port
}

func (auth *Authenticator) Authenticate() (string, error) {
	conn, err := auth.listener.Accept()
	if err != nil {
		return "", err
	}
	buf := make([]byte, 4096)
	bufsz := 0
	for bufsz < 4096 {
		n, err := conn.Read(buf[bufsz:])
		if err != nil && err != io.EOF {
			return "", err
		}
		bufsz += n
		req := string(buf[:bufsz])
		if strings.HasSuffix(req, "\r\n\r\n") {
			if code, err := extractCode(req, auth.state); err != nil {
				return "", err
			} else {
				if _, err := conn.Write([]byte(authResponse)); err != nil {
					return "", err
				}
				return getToken(code, auth.Port())
			}
		}
	}
	return "", errInvalidAuthResponse
}

func getToken(code string, port int) (string, error) {
	tokenParams := url.Values{}
	tokenParams.Add("client_id", CLIENT_ID)
	tokenParams.Add("client_secret", CLIENT_SECRET)
	tokenParams.Add("code", code)
	tokenParams.Add("grant_type", "authorization_code")
	tokenParams.Add("redirect_uri", fmt.Sprintf("http://localhost:%v", port))

	var tokenResponse TokenResponse

	err := httpRequest(
		http.MethodPost,
		"https://oauth2.googleapis.com/token",
		map[string]string{"Content-Type": "application/x-www-form-urlencoded"},
		strings.NewReader(tokenParams.Encode()),
		&tokenResponse,
	)
	if err != nil {
		return "", err
	} else {
		return tokenResponse.AccessToken, nil
	}
}

func extractCode(request, state string) (string, error) {
	rxRequestLine := regexp.MustCompile(`GET (\S+) HTTP/[12]\.[01]`)
	matches := rxRequestLine.FindStringSubmatch(request)
	if matches == nil {
		return "", errInvalidAuthResponse
	}
	url, err := url.Parse(matches[1])
	if err != nil {
		return "", err
	}
	query := url.Query()
	if query.Has("error") {
		return "", fmt.Errorf("authorization failed: %v", query.Get("error"))
	}
	if !query.Has("code") || !query.Has("state") {
		return "", errInvalidAuthResponse
	}
	if state != query.Get("state") {
		return "", errInvalidAuthResponse
	}
	return query.Get("code"), nil
}

func (auth *Authenticator) URL() string {
	redirect_uri := fmt.Sprintf("http://localhost:%v", auth.Port())
	params := url.Values{}
	params.Add("client_id", CLIENT_ID)
	params.Add("redirect_uri", redirect_uri)
	params.Add("response_type", "code")
	params.Add("scope", "https://www.googleapis.com/auth/gmail.readonly")
	params.Add("access_type", "offline")
	params.Add("state", auth.state)
	return fmt.Sprintf("https://accounts.google.com/o/oauth2/v2/auth?%s", params.Encode())
}
