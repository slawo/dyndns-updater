package updater

import (
	"context"
	"crypto/rand"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestURLSubstitution(t *testing.T) {
	token := rand.Text() + "&=+/ ?#%"
	c := config{update: "https://provider.invalid/api/{record}?hostname={hostname}&token={token}&address={ip}", parameters: map[string]string{"record": "a/b ?#", "hostname": "router.example", "token": token}}
	raw, err := c.renderURL("8.8.8.8")
	if err != nil {
		t.Fatal(err)
	}
	u, err := url.Parse(raw)
	if err != nil {
		t.Fatal(err)
	}
	if u.Host != "provider.invalid" || !strings.Contains(u.EscapedPath(), "a%2Fb%20%3F%23") || u.Query().Get("hostname") != "router.example" || u.Query().Get("token") != token || u.Query().Get("address") != "8.8.8.8" || len(u.Query()) != 3 {
		t.Fatal("substitution changed URL structure or values")
	}
}

func TestRejectInvalidUpdateConfiguration(t *testing.T) {
	for name, c := range map[string]config{
		"missing parameter": {update: "https://provider.invalid/?host={hostname}&ip={ip}"},
		"host substitution": {update: "https://{hostname}/?ip={ip}", parameters: map[string]string{"hostname": "provider.invalid"}},
		"http":              {update: "http://provider.invalid/?ip={ip}"},
		"userinfo":          {update: "https://user@provider.invalid/?ip={ip}"},
		"fragment":          {update: "https://provider.invalid/?ip={ip}#fragment"},
		"missing IP":        {update: "https://provider.invalid/"},
		"bad placeholder":   {update: "https://provider.invalid/?ip={ip}&value={not-valid}"},
		"reserved IP":       {update: "https://provider.invalid/?ip={ip}", parameters: map[string]string{"ip": "1.1.1.1"}},
		"bad key":           {update: "https://provider.invalid/?ip={ip}", parameters: map[string]string{"bad-key": "value"}},
		"bad regex":         {update: "https://provider.invalid/?ip={ip}", success: "["},
		"header injection":  {update: "https://provider.invalid/?ip={ip}", headers: map[string]string{"Authorization": "Bearer {token}"}, parameters: map[string]string{"token": rand.Text() + "\r\nX-Injected: yes"}},
		"bad header":        {update: "https://provider.invalid/?ip={ip}", headers: map[string]string{"bad header": "value"}},
		"host header":       {update: "https://provider.invalid/?ip={ip}", headers: map[string]string{"Host": "elsewhere.invalid"}},
		"conflicting auth":  {update: "https://provider.invalid/?ip={ip}", headers: map[string]string{"Authorization": "Bearer {token}"}, parameters: map[string]string{"token": rand.Text()}, user: rand.Text(), password: rand.Text()},
		"duplicate header":  {update: "https://provider.invalid/?ip={ip}", headers: map[string]string{"x-value": "a", "X-Value": "b"}},
		"bad method":        {update: "https://provider.invalid/?ip={ip}", method: "DELETE"},
		"GET body":          {update: "https://provider.invalid/", method: "GET", body: `{"ip":"{ip}"}`},
		"invalid JSON":      {update: "https://provider.invalid/", method: "PATCH", body: `{"ip":{ip}}`},
	} {
		t.Run(name, func(t *testing.T) {
			if c.validateUpdate() == nil {
				t.Fatal("accepted invalid update configuration")
			}
		})
	}
}

func TestSuccessRules(t *testing.T) {
	for _, body := range []string{"good 8.8.8.8", "nochg 8.8.8.8", "good", "good 8.8.8.8\nnochg 8.8.8.8"} {
		if !(config{}).confirm(body, "8.8.8.8") {
			t.Fatalf("rejected valid DynDNS2 response: %q", body)
		}
	}
	for _, body := range []string{"", "badauth", "good 1.1.1.1", "good 8.8.8.8\nnohost", "OK", "<html>good 8.8.8.8</html>"} {
		if (config{}).confirm(body, "8.8.8.8") {
			t.Fatalf("accepted failure response: %q", body)
		}
	}
	for _, tc := range []struct {
		pattern, body string
		ok            bool
	}{
		{"OK", "OK", true}, {"OK", "KO", false}, {"OK", "not OK", false},
		{"addresses (updated|unchanged)", "addresses updated", true},
		{`(?s).*<IP>{ip}</IP>.*<ErrCount>0</ErrCount>.*<Done>true</Done>.*`, `<interface-response><IP>8.8.8.8</IP><ErrCount>0</ErrCount><Done>true</Done></interface-response>`, true},
		{`(?s).*<ErrCount>0</ErrCount>.*`, `<ErrCount>1</ErrCount>`, false},
		{`good {ip}`, "good 8x8x8x8", false},
		{`(?s).*"success"\s*:\s*true.*`, `{"success":true}`, true},
		{`(?s).*"success"\s*:\s*true.*`, `{"success":false}`, false},
		{`^$`, "", true},
	} {
		if got := (config{success: tc.pattern}).confirm(tc.body, "8.8.8.8"); got != tc.ok {
			t.Errorf("unexpected result for %q", tc.pattern)
		}
	}
}

func TestJSONAndFormRequests(t *testing.T) {
	for _, method := range []string{http.MethodPost, http.MethodPut, http.MethodPatch} {
		t.Run(method, func(t *testing.T) {
			token := rand.Text()
			label := "quoted \"value\" & newline\n\x00"
			server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Method != method || r.Header.Get("Authorization") != "Bearer "+token || r.Header.Get("Content-Type") != "application/json" {
					t.Error("incorrect method or headers")
				}
				var body map[string]string
				if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
					t.Error(err)
				}
				if body["content"] != "8.8.8.8" || body["label"] != label {
					t.Error("incorrect JSON substitution")
				}
				w.WriteHeader(http.StatusCreated)
				_, _ = w.Write([]byte(`{"success":true}`))
			}))
			defer server.Close()
			c := config{update: server.URL, method: method, body: `{"content":"{ip}","label":"{label}"}`, parameters: map[string]string{"token": token, "label": label}, headers: map[string]string{"Authorization": "Bearer {token}"}, success: `(?s).*"success"\s*:\s*true.*`}
			if err := c.validateUpdate(); err != nil {
				t.Fatal(err)
			}
			if err := c.publish(context.Background(), server.Client(), "8.8.8.8"); err != nil {
				t.Fatal(err)
			}
		})
	}
	c := config{update: "https://provider.invalid/", method: "POST", body: "address={ip}&token={token}", parameters: map[string]string{"token": rand.Text() + "&+="}, headers: map[string]string{"Content-Type": "application/x-www-form-urlencoded"}}
	_, body, _, err := c.renderRequest("8.8.8.8")
	if err != nil {
		t.Fatal(err)
	}
	values, err := url.ParseQuery(body)
	if err != nil || values.Get("token") != c.parameters["token"] || len(values) != 2 {
		t.Fatal("form substitution altered parameters")
	}
}

func TestCustomResponseStateAndCredentialIsolation(t *testing.T) {
	token := rand.Text()
	fail := true
	state := filepath.Join(t.TempDir(), "state")
	var lookups, updates int
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/lookup" {
			lookups++
			if r.Header.Get("Authorization") != "" || r.URL.Query().Get("token") != "" {
				t.Error("credentials leaked to lookup")
			}
			_, _ = w.Write([]byte("8.8.8.8"))
			return
		}
		updates++
		if r.URL.Query().Get("token") != token || r.URL.Query().Get("address") != "8.8.8.8" {
			t.Error("incorrect update values")
		}
		if fail {
			w.WriteHeader(http.StatusUnauthorized)
			_, _ = w.Write([]byte("OK"))
			return
		}
		_, _ = w.Write([]byte("OK"))
	}))
	defer server.Close()
	c := config{urls: []string{server.URL + "/lookup"}, update: server.URL + "/update?token={token}&address={ip}", parameters: map[string]string{"token": token}, success: "OK", state: state}
	w := worker{config: c, client: server.Client()}
	if w.tick(context.Background()) == nil {
		t.Fatal("accepted non-2xx response")
	}
	if _, err := os.Stat(state); !os.IsNotExist(err) {
		t.Fatal("saved failed update")
	}
	fail = false
	if err := w.tick(context.Background()); err != nil {
		t.Fatal(err)
	}
	if err := w.tick(context.Background()); err != nil {
		t.Fatal(err)
	}
	if lookups != 3 || updates != 2 {
		t.Fatal("unexpected lookup/update count")
	}
}

func TestTemplateEnvironmentConfiguration(t *testing.T) {
	t.Setenv("DYNDNS_UPDATE_URL", "https://provider.invalid/update?hostname={hostname}&token={token}&ip={ip}")
	t.Setenv("DYNDNS_STATE_FILE", filepath.Join(t.TempDir(), "state"))
	t.Setenv("DYNDNS_LOOKUP_URLS", "https://api.ipify.org")
	t.Setenv("DYNDNS_USERNAME", "")
	t.Setenv("DYNDNS_PASSWORD", "")
	t.Setenv("DYNDNS_UPDATE_METHOD", "")
	t.Setenv("DYNDNS_UPDATE_BODY", "")
	t.Setenv("DYNDNS_UPDATE_HEADERS", "")
	t.Setenv("DYNDNS_SUCCESS_REGEX", "OK")
	parameters, err := json.Marshal(map[string]string{"hostname": "router.example", "token": rand.Text()})
	if err != nil {
		t.Fatal(err)
	}
	t.Setenv("DYNDNS_PARAMETERS", string(parameters))
	if _, err := loadConfig(); err != nil {
		t.Fatal(err)
	}
	for _, raw := range []string{"[]", "null", "{", `{"token":123}`, `{"hostname":"router.example"}`} {
		t.Setenv("DYNDNS_PARAMETERS", raw)
		if _, err := loadConfig(); err == nil {
			t.Fatal("accepted invalid or incomplete parameters")
		}
	}
	t.Setenv("DYNDNS_PARAMETERS", string(parameters))
	t.Setenv("DYNDNS_UPDATE_HEADERS", "[]")
	if _, err := loadConfig(); err == nil {
		t.Fatal("accepted invalid headers object")
	}
}

func TestMissingUpdateURLIsNamed(t *testing.T) {
	err := (config{}).validateUpdate()
	if err == nil || !strings.Contains(err.Error(), "DYNDNS_UPDATE_URL") {
		t.Fatal("missing URL error must name its configuration key")
	}
}
