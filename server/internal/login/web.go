package login

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"log/slog"
	"net/http"
)

// The web login endpoint. Formats are taken from the 0.8 client's parsers
// (__ParseJsonAuthGet, __ExtractJsonAuthGetInputs, __SubmitLogin, __ParseJsonAuthPost):
//
// Every response must set the cookies JSESSIONID and web.id: the client finds
// them by searching the raw response headers for "JSESSIONID" / "web.id" and
// reads each value up to the next ';' (FUN_7ff6510b8e00), failing with
// "invalid response received" if either is missing.
//
// GET  -> {"type":"LOGIN_FORM","inputs":[{"input_id","label","max_length"},...]}
//         Accepted input_ids: account_name, password, captcha, log_in_submit.
//         max_length is required for the text fields. An optional "captcha_url"
//         switches the client to its captcha flow.
// POST <- {"version","program_id","platform_id","inputs":[{"input_id","value"},...]}
//      -> {"authentication_state":"DONE","login_ticket":"..."}
//         or "LOGIN" with "error_code"/"error_message" to ask again.
//         (LEGAL, AUTHENTICATOR and CAPTCHA_REQUIRED also exist; unused.)

type formInput struct {
	InputID   string `json:"input_id"`
	Type      string `json:"type"`
	Label     string `json:"label"`
	MaxLength int    `json:"max_length,omitempty"`
}

type loginFormJSON struct {
	Type   string      `json:"type"`
	Inputs []formInput `json:"inputs"`
}

// fullLoginForm asks for an e-mail and password (any values are accepted).
var fullLoginForm = loginFormJSON{
	Type: "LOGIN_FORM",
	Inputs: []formInput{
		{InputID: "account_name", Type: "text", Label: "E-mail", MaxLength: 320},
		{InputID: "password", Type: "password", Label: "Password", MaxLength: 128},
		{InputID: "log_in_submit", Type: "submit", Label: "Log In"},
	},
}

// buttonLoginForm is just a button; the account is Service.DefaultAccount.
var buttonLoginForm = loginFormJSON{
	Type:   "LOGIN_FORM",
	Inputs: []formInput{{InputID: "log_in_submit", Type: "submit", Label: "Play"}},
}

type loginSubmit struct {
	Version    string `json:"version"`
	ProgramID  string `json:"program_id"`
	PlatformID string `json:"platform_id"`
	Inputs     []struct {
		InputID string `json:"input_id"`
		Value   string `json:"value"`
	} `json:"inputs"`
}

type loginResult struct {
	AuthenticationState string `json:"authentication_state"`
	LoginTicket         string `json:"login_ticket,omitempty"`
	ErrorCode           string `json:"error_code,omitempty"`
	ErrorMessage        string `json:"error_message,omitempty"`
}

// WebHandler serves the login form and accepts the credentials. It answers on
// any path, so the path in WebURL is free to choose.
func (s *Service) WebHandler(log *slog.Logger) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		log := log.With("remote", r.RemoteAddr)
		setSessionCookies(w, r)
		switch r.Method {
		case http.MethodGet:
			form := fullLoginForm
			if s.DefaultAccount != "" {
				form = buttonLoginForm
			}
			log.Info("web login: sending login form", "path", r.URL.Path, "button_only", s.DefaultAccount != "")
			writeJSON(w, form)

		case http.MethodPost:
			var submit loginSubmit
			if err := json.NewDecoder(r.Body).Decode(&submit); err != nil {
				log.Warn("web login: bad request body", "err", err)
				http.Error(w, "bad request", http.StatusBadRequest)
				return
			}
			var account string
			for _, in := range submit.Inputs {
				if in.InputID == "account_name" {
					account = in.Value
				}
			}
			if account == "" {
				account = s.DefaultAccount
			}
			if account == "" {
				log.Info("web login: no account name, asking again")
				writeJSON(w, loginResult{AuthenticationState: "LOGIN", ErrorCode: "INVALID_ACCOUNT_OR_CREDENTIALS", ErrorMessage: "Enter an account name."})
				return
			}
			log.Info("web login: credentials accepted", "account", account,
				"program", submit.ProgramID, "platform", submit.PlatformID, "version", submit.Version)
			writeJSON(w, loginResult{AuthenticationState: "DONE", LoginTicket: s.issueTicket(account)})

		default:
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		}
	})
}

// setSessionCookies sets JSESSIONID and web.id, keeping values the client sent back.
// The Path attribute also provides the ';' that terminates each value.
func setSessionCookies(w http.ResponseWriter, r *http.Request) {
	for _, name := range []string{"JSESSIONID", "web.id"} {
		value := ""
		if c, err := r.Cookie(name); err == nil {
			value = c.Value
		} else {
			buf := make([]byte, 16)
			rand.Read(buf)
			value = hex.EncodeToString(buf)
		}
		http.SetCookie(w, &http.Cookie{Name: name, Value: value, Path: "/"})
	}
}

func writeJSON(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json;charset=utf-8")
	json.NewEncoder(w).Encode(v)
}
