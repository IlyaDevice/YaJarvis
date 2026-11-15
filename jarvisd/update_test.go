package main

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

type updateTransport struct{ base string }

func (t updateTransport) RoundTrip(r *http.Request) (*http.Response, error) {
	clone := r.Clone(r.Context())
	clone.URL.Scheme = "http"
	clone.URL.Host = strings.TrimPrefix(t.base, "http://")
	return http.DefaultTransport.RoundTrip(clone)
}

func TestUpdateFromGitHubRelease(t *testing.T) {
	sum := strings.Repeat("a", 64)
	withSum := true
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/repos/newfpv/YaJavis/releases/latest":
			assets := fmt.Sprintf(`{"name":%q,"browser_download_url":"https://github.com/update"}`, assetName())
			if withSum {
				assets += `,{"name":"SHA256SUMS","browser_download_url":"https://github.com/sums"}`
			}
			fmt.Fprintf(w, `{"tag_name":"v99.0.0","body":"Release notes","assets":[%s]}`, assets)
		case "/sums":
			fmt.Fprintf(w, "%s  %s\n", sum, assetName())
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()
	st, err := OpenStore(t.TempDir() + "/settings.json")
	if err != nil {
		t.Fatal(err)
	}
	client := &http.Client{Transport: updateTransport{base: srv.URL}}
	u := &Updater{store: st, client: func() *http.Client { return client }}
	if err := u.Check(context.Background()); err != nil {
		t.Fatal(err)
	}
	if u.latest != "v99.0.0" || u.sum != sum || u.asset != "https://github.com/update" || !u.Available() {
		t.Fatal("release version, architecture asset or checksum not selected")
	}
	withSum = false
	if err := u.Check(context.Background()); err == nil {
		t.Fatal("accepted release without checksum")
	}
}
