package jenkins

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// cascTestServer returns an httptest server whose handler 404s the CSRF
// crumb probe (so SetCrumb harmlessly skips) and delegates every other request
// to h.
func cascTestServer(h http.HandlerFunc) *httptest.Server {
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.Contains(r.URL.Path, "crumbIssuer") {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		h(w, r)
	}))
}

func TestApplyCASC(t *testing.T) {
	t.Run("success posts the YAML body", func(t *testing.T) {
		var gotMethod, gotPath, gotCT, gotBody string
		srv := cascTestServer(func(w http.ResponseWriter, r *http.Request) {
			gotMethod, gotPath = r.Method, r.URL.Path
			gotCT = r.Header.Get("Content-Type")
			b, _ := io.ReadAll(r.Body)
			gotBody = string(b)
			w.Header().Set("Content-Type", "text/plain; charset=utf-8")
			_, _ = io.WriteString(w, "Configuration successfully applied.")
		})
		defer srv.Close()

		c, _ := newJenkinsClient(&Config{ServerURL: srv.URL})
		if err := c.ApplyCASC(context.Background(), "jenkins:\n  systemMessage: hi\n"); err != nil {
			t.Fatalf("ApplyCASC error = %v", err)
		}
		if gotMethod != http.MethodPost {
			t.Errorf("method = %q, want POST", gotMethod)
		}
		if gotPath != "/configuration-as-code/configure" {
			t.Errorf("path = %q, want /configuration-as-code/configure", gotPath)
		}
		if !strings.HasPrefix(gotCT, "application/x-yaml") {
			t.Errorf("Content-Type = %q, want application/x-yaml", gotCT)
		}
		if !strings.Contains(gotBody, "systemMessage: hi") {
			t.Errorf("posted body = %q, missing the declared YAML", gotBody)
		}
	})

	t.Run("failure surfaces the JCasC error body", func(t *testing.T) {
		srv := cascTestServer(func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusBadRequest)
			_, _ = io.WriteString(w, `[{"line":-1,"message":"No configurator for root element 'bogus'"}]`)
		})
		defer srv.Close()

		c, _ := newJenkinsClient(&Config{ServerURL: srv.URL})
		err := c.ApplyCASC(context.Background(), "bogus: {}\n")
		if err == nil {
			t.Fatal("expected an error for a 400 response")
		}
		if !strings.Contains(err.Error(), "No configurator") {
			t.Errorf("error should surface the JCasC message, got %v", err)
		}
	})
}

func TestExportCASC(t *testing.T) {
	const exported = "jenkins:\n  systemMessage: \"hello\"\n  numExecutors: 2\n"

	t.Run("returns the YAML body", func(t *testing.T) {
		var gotMethod, gotPath string
		srv := cascTestServer(func(w http.ResponseWriter, r *http.Request) {
			gotMethod, gotPath = r.Method, r.URL.Path
			w.Header().Set("Content-Type", "application/x-yaml; charset=utf-8")
			_, _ = io.WriteString(w, exported)
		})
		defer srv.Close()

		c, _ := newJenkinsClient(&Config{ServerURL: srv.URL})
		out, err := c.ExportCASC(context.Background())
		if err != nil {
			t.Fatalf("ExportCASC error = %v", err)
		}
		if gotMethod != http.MethodPost {
			t.Errorf("method = %q, want POST", gotMethod)
		}
		if gotPath != "/configuration-as-code/export" {
			t.Errorf("path = %q, want /configuration-as-code/export", gotPath)
		}
		if out != exported {
			t.Errorf("export = %q, want %q", out, exported)
		}
	})

	t.Run("non-200 errors", func(t *testing.T) {
		srv := cascTestServer(func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusForbidden)
		})
		defer srv.Close()

		c, _ := newJenkinsClient(&Config{ServerURL: srv.URL})
		if _, err := c.ExportCASC(context.Background()); err == nil {
			t.Fatal("expected an error for a 403 response")
		}
	})
}
