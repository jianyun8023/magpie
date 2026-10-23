package gui

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"reflect"
	"sort"
	"sync"
	"testing"

	"github.com/yetone/magpie/internal/provider"
)

// #3170: the editor's explicit URLs override saved ones without deriving
// one protocol's endpoint from another, for both detection buttons.
func TestProviderDetectCurrentEndpoints(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	t.Setenv("USERPROFILE", os.Getenv("HOME"))
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	t.Setenv("XDG_CACHE_HOME", t.TempDir())
	var mu sync.Mutex
	var asked []string
	up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		io.Copy(io.Discard, r.Body)
		if r.Method == http.MethodPost {
			mu.Lock()
			asked = append(asked, r.URL.Path)
			mu.Unlock()
		}
		io.WriteString(w, `{}`)
	}))
	defer up.Close()
	mux := http.NewServeMux()
	providerRoutes(mux, nil)
	post := func(action string, body map[string]any) []byte {
		t.Helper()
		b, err := json.Marshal(body)
		if err != nil {
			t.Fatal(err)
		}
		w := httptest.NewRecorder()
		mux.ServeHTTP(w, httptest.NewRequest("POST", "/api/provider/"+action, bytes.NewReader(b)))
		if w.Code != http.StatusOK {
			t.Fatalf("%s: %d %s", action, w.Code, w.Body)
		}
		return w.Body.Bytes()
	}
	post("save", map[string]any{
		"id": "endpoint-relay", "name": "Endpoint relay", "new": true, "key": "sk-test",
		"chat": up.URL + "/old/chat", "responses": up.URL + "/old/responses",
		"anthropic": up.URL + "/old/anthropic", "gemini": up.URL + "/old/gemini",
		"models": []string{"m"},
	})
	for _, shared := range []bool{false, true} {
		for _, each := range []bool{false, true} {
			bases := map[provider.Protocol]string{
				provider.Chat: up.URL + "/chat/custom", provider.Responses: up.URL + "/responses/custom/v1",
				provider.Anthropic: up.URL + "/anthropic/custom", provider.Gemini: up.URL + "/gemini/custom/v1beta",
			}
			if shared {
				bases[provider.Responses] = bases[provider.Chat]
			}
			body := map[string]any{"id": "endpoint-relay", "typed": true, "base": up.URL + "/wrong/v1", "model": "m"}
			for proto, base := range bases {
				body[string(proto)] = base
			}
			if each {
				body["detectModels"] = []string{"m"}
			}
			mu.Lock()
			asked = nil
			mu.Unlock()
			var got struct {
				Results []provider.Detection
				Models  []provider.ModelDetection
			}
			if err := json.Unmarshal(post("detect", body), &got); err != nil {
				t.Fatal(err)
			}
			if len(got.Results) != len(bases) || (each && len(got.Models) != 1) {
				t.Fatalf("shared=%v each=%v: %+v", shared, each, got)
			}
			for _, r := range got.Results {
				if !r.OK || r.Base != bases[r.Protocol] {
					t.Fatalf("shared=%v each=%v: %+v", shared, each, r)
				}
			}
			want := []string{"/chat/custom/chat/completions", "/responses/custom/v1/responses", "/anthropic/custom/v1/messages", "/gemini/custom/v1beta/models/m:generateContent"}
			if shared {
				want[1] = "/chat/custom/responses"
			}
			sort.Strings(want)
			mu.Lock()
			paths := append([]string(nil), asked...)
			mu.Unlock()
			sort.Strings(paths)
			if !reflect.DeepEqual(paths, want) {
				t.Fatalf("shared=%v each=%v: asked %v, want %v", shared, each, paths, want)
			}
		}
	}
	p, err := provider.Find("endpoint-relay")
	if err != nil || p.Chat != up.URL+"/old/chat" || p.Responses != up.URL+"/old/responses" || p.Anthropic != up.URL+"/old/anthropic" || p.Gemini != up.URL+"/old/gemini" {
		t.Fatalf("detection changed saved endpoints: %+v, %v", p, err)
	}
}
