package updater

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestRotationPublishAndRestartState(t *testing.T) {
	ip := "8.8.8.8"
	calls := 0
	fail := true
	var paths []string
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		paths = append(paths, r.URL.Path)
		if r.URL.Path == "/update" {
			calls++
			if fail {
				_, _ = w.Write([]byte("badauth"))
				return
			}
			_, _ = w.Write([]byte("good " + r.URL.Query().Get("myip")))
			return
		}
		_, _ = w.Write([]byte(ip))
	}))
	defer server.Close()
	path := filepath.Join(t.TempDir(), "state")
	w := worker{config: config{urls: []string{server.URL + "/a", server.URL + "/b"}, update: server.URL + "/update?myip={ip}", state: path}, client: server.Client()}
	if err := w.tick(context.Background()); err == nil {
		t.Fatal("accepted failed update")
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatal("saved failed update")
	}
	fail = false
	if err := w.tick(context.Background()); err != nil {
		t.Fatal(err)
	}
	if paths[0] != "/a" || paths[2] != "/b" {
		t.Fatal(paths)
	}
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	w.last = strings.TrimSpace(string(b))
	if err := w.tick(context.Background()); err != nil {
		t.Fatal(err)
	}
	if calls != 2 {
		t.Fatal("republished unchanged IP")
	}
	ip = "1.1.1.1"
	if err := w.tick(context.Background()); err != nil {
		t.Fatal(err)
	}
	if calls != 3 {
		t.Fatal("missed changed IP")
	}
}
func TestRejectInvalidIPv4(t *testing.T) {
	for _, v := range []string{"::1", "192.168.1.1", "100.64.1.1", "127.0.0.1", "<html>8.8.8.8</html>", "8.8.8.8 garbage", "999.1.1.1", "0.0.0.0"} {
		if _, err := publicIPv4(v); err == nil {
			t.Errorf("accepted %q", v)
		}
	}
}
func TestConfig(t *testing.T) {
	t.Setenv("DYNDNS_UPDATE_URL", "https://provider.invalid/nic/update?myip={ip}")
	t.Setenv("DYNDNS_STATE_FILE", filepath.Join(t.TempDir(), "state"))
	t.Setenv("DYNDNS_LOOKUP_URLS", "https://api.ipify.org,https://ifconfig.me/ip")
	c, err := loadConfig()
	if err != nil || len(c.urls) != 2 {
		t.Fatal("unexpected configuration or URL count", err)
	}
	t.Setenv("DYNDNS_LOOKUP_URLS", "")
	file := filepath.Join(t.TempDir(), "urls")
	if err := os.WriteFile(file, []byte("# sources\nhttps://api.ipify.org\n"), 0600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("DYNDNS_LOOKUP_FILE", file)
	c, err = loadConfig()
	if err != nil || len(c.urls) != 1 {
		t.Fatal("unexpected configuration or URL count", err)
	}
	t.Setenv("DYNDNS_INTERVAL", "0s")
	if _, err := loadConfig(); err == nil {
		t.Fatal("accepted zero interval")
	}
}
