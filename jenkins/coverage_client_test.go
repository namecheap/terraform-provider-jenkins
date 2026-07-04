package jenkins

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// covBServer returns an httptest server whose handler 404s the CSRF crumb probe
// (so Requester.SetCrumb harmlessly skips) and delegates every other request to
// h. It mirrors cascTestServer but is named uniquely for this file.
func covBServer(h http.HandlerFunc) *httptest.Server {
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.Contains(r.URL.Path, "crumbIssuer") {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		h(w, r)
	}))
}

func covBClient(t *testing.T, srv *httptest.Server) *jenkinsAdapter {
	t.Helper()
	c, err := newJenkinsClient(&Config{ServerURL: srv.URL})
	if err != nil {
		t.Fatalf("newJenkinsClient error = %v", err)
	}
	return c
}

// --- credentialStoreBase (pure helper) -------------------------------------

func TestCovB_CredentialStoreBase(t *testing.T) {
	if got := credentialStoreBase(""); got != "/credentials/store/system" {
		t.Errorf("global base = %q, want /credentials/store/system", got)
	}
	if got := credentialStoreBase("myfolder"); got != "/job/myfolder/credentials/store/folder" {
		t.Errorf("folder base = %q, want /job/myfolder/credentials/store/folder", got)
	}
	if got := credentialStoreBase("a/b"); got != "/job/a/job/b/credentials/store/folder" {
		t.Errorf("nested base = %q, want /job/a/job/b/credentials/store/folder", got)
	}
}

// --- credential domain ------------------------------------------------------

func TestCovB_CreateCredentialDomain(t *testing.T) {
	t.Run("success posts XML to createDomain", func(t *testing.T) {
		var gotMethod, gotPath, gotBody string
		srv := covBServer(func(w http.ResponseWriter, r *http.Request) {
			gotMethod, gotPath = r.Method, r.URL.Path
			b, _ := io.ReadAll(r.Body)
			gotBody = string(b)
			w.WriteHeader(http.StatusOK)
		})
		defer srv.Close()

		c := covBClient(t, srv)
		if err := c.CreateCredentialDomain(context.Background(), "", "mydomain", "desc"); err != nil {
			t.Fatalf("CreateCredentialDomain error = %v", err)
		}
		if gotMethod != http.MethodPost {
			t.Errorf("method = %q, want POST", gotMethod)
		}
		if gotPath != "/credentials/store/system/createDomain" {
			t.Errorf("path = %q, want /credentials/store/system/createDomain", gotPath)
		}
		if !strings.Contains(gotBody, "<name>mydomain</name>") {
			t.Errorf("body = %q, missing domain name", gotBody)
		}
	})

	t.Run("conflict reports already exists", func(t *testing.T) {
		srv := covBServer(func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusConflict)
		})
		defer srv.Close()

		c := covBClient(t, srv)
		err := c.CreateCredentialDomain(context.Background(), "", "dup", "")
		if err == nil || !strings.Contains(err.Error(), "already exists") {
			t.Fatalf("error = %v, want 'already exists'", err)
		}
	})

	t.Run("other status errors", func(t *testing.T) {
		srv := covBServer(func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusInternalServerError)
		})
		defer srv.Close()

		c := covBClient(t, srv)
		if err := c.CreateCredentialDomain(context.Background(), "afolder", "x", ""); err == nil {
			t.Fatal("expected an error for a 500 response")
		}
	})
}

func TestCovB_GetCredentialDomain(t *testing.T) {
	t.Run("success decodes config.xml", func(t *testing.T) {
		var gotPath string
		srv := covBServer(func(w http.ResponseWriter, r *http.Request) {
			gotPath = r.URL.Path
			w.Header().Set("Content-Type", "application/xml")
			_, _ = io.WriteString(w, `<?xml version='1.1' encoding='UTF-8'?>
<com.cloudbees.plugins.credentials.domains.Domain>
  <name>mydomain</name>
  <description>a description</description>
  <specifications/>
</com.cloudbees.plugins.credentials.domains.Domain>`)
		})
		defer srv.Close()

		c := covBClient(t, srv)
		var out credentialDomainXML
		if err := c.GetCredentialDomain(context.Background(), "", "mydomain", &out); err != nil {
			t.Fatalf("GetCredentialDomain error = %v", err)
		}
		if gotPath != "/credentials/store/system/domain/mydomain/config.xml" {
			t.Errorf("path = %q", gotPath)
		}
		if out.Name != "mydomain" || out.Description != "a description" {
			t.Errorf("decoded = %+v", out)
		}
	})

	t.Run("404 surfaces not found", func(t *testing.T) {
		srv := covBServer(func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusNotFound)
		})
		defer srv.Close()

		c := covBClient(t, srv)
		var out credentialDomainXML
		err := c.GetCredentialDomain(context.Background(), "", "missing", &out)
		if err == nil || !strings.Contains(err.Error(), "not found") {
			t.Fatalf("error = %v, want not found", err)
		}
	})
}

func TestCovB_UpdateCredentialDomain(t *testing.T) {
	t.Run("success posts to config.xml", func(t *testing.T) {
		var gotMethod, gotPath string
		srv := covBServer(func(w http.ResponseWriter, r *http.Request) {
			gotMethod, gotPath = r.Method, r.URL.Path
			w.WriteHeader(http.StatusOK)
		})
		defer srv.Close()

		c := covBClient(t, srv)
		if err := c.UpdateCredentialDomain(context.Background(), "", "mydomain", "new desc"); err != nil {
			t.Fatalf("UpdateCredentialDomain error = %v", err)
		}
		if gotMethod != http.MethodPost {
			t.Errorf("method = %q, want POST", gotMethod)
		}
		if gotPath != "/credentials/store/system/domain/mydomain/config.xml" {
			t.Errorf("path = %q", gotPath)
		}
	})

	t.Run("non-200 errors", func(t *testing.T) {
		srv := covBServer(func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusInternalServerError)
		})
		defer srv.Close()

		c := covBClient(t, srv)
		if err := c.UpdateCredentialDomain(context.Background(), "", "mydomain", ""); err == nil {
			t.Fatal("expected an error for a 500 response")
		}
	})
}

func TestCovB_DeleteCredentialDomain(t *testing.T) {
	t.Run("success posts to doDelete", func(t *testing.T) {
		var gotPath string
		srv := covBServer(func(w http.ResponseWriter, r *http.Request) {
			gotPath = r.URL.Path
			w.WriteHeader(http.StatusOK)
		})
		defer srv.Close()

		c := covBClient(t, srv)
		if err := c.DeleteCredentialDomain(context.Background(), "", "mydomain"); err != nil {
			t.Fatalf("DeleteCredentialDomain error = %v", err)
		}
		if gotPath != "/credentials/store/system/domain/mydomain/doDelete" {
			t.Errorf("path = %q", gotPath)
		}
	})

	t.Run("non-200 errors", func(t *testing.T) {
		srv := covBServer(func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusInternalServerError)
		})
		defer srv.Close()

		c := covBClient(t, srv)
		if err := c.DeleteCredentialDomain(context.Background(), "", "mydomain"); err == nil {
			t.Fatal("expected an error for a 500 response")
		}
	})
}

// --- role strategy ----------------------------------------------------------

func TestCovB_AddRole(t *testing.T) {
	t.Run("success posts addRole form", func(t *testing.T) {
		var gotPath, gotBody string
		srv := covBServer(func(w http.ResponseWriter, r *http.Request) {
			gotPath = r.URL.Path
			b, _ := io.ReadAll(r.Body)
			gotBody = string(b)
			w.WriteHeader(http.StatusOK)
		})
		defer srv.Close()

		c := covBClient(t, srv)
		err := c.AddRole(context.Background(), "globalRoles", "admin", []string{"hudson.model.Hudson.Administer"}, ".*", true)
		if err != nil {
			t.Fatalf("AddRole error = %v", err)
		}
		if gotPath != "/role-strategy/strategy/addRole" {
			t.Errorf("path = %q", gotPath)
		}
		for _, want := range []string{"roleName=admin", "type=globalRoles", "overwrite=true", "pattern=.%2A"} {
			if !strings.Contains(gotBody, want) {
				t.Errorf("form body = %q, missing %q", gotBody, want)
			}
		}
	})

	t.Run("non-200 errors", func(t *testing.T) {
		srv := covBServer(func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusInternalServerError)
		})
		defer srv.Close()

		c := covBClient(t, srv)
		err := c.AddRole(context.Background(), "globalRoles", "admin", nil, "", false)
		if err == nil {
			t.Fatal("expected an error for a 500 response")
		}
	})
}

func TestCovB_AssignRole(t *testing.T) {
	var gotPath, gotBody string
	srv := covBServer(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		b, _ := io.ReadAll(r.Body)
		gotBody = string(b)
		w.WriteHeader(http.StatusOK)
	})
	defer srv.Close()

	c := covBClient(t, srv)
	if err := c.AssignRole(context.Background(), "globalRoles", "admin", "alice"); err != nil {
		t.Fatalf("AssignRole error = %v", err)
	}
	if gotPath != "/role-strategy/strategy/assignRole" {
		t.Errorf("path = %q", gotPath)
	}
	if !strings.Contains(gotBody, "sid=alice") {
		t.Errorf("form body = %q, missing sid", gotBody)
	}
}

func TestCovB_RemoveRole(t *testing.T) {
	var gotPath, gotBody string
	srv := covBServer(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		b, _ := io.ReadAll(r.Body)
		gotBody = string(b)
		w.WriteHeader(http.StatusOK)
	})
	defer srv.Close()

	c := covBClient(t, srv)
	if err := c.RemoveRole(context.Background(), "globalRoles", "admin"); err != nil {
		t.Fatalf("RemoveRole error = %v", err)
	}
	if gotPath != "/role-strategy/strategy/removeRoles" {
		t.Errorf("path = %q", gotPath)
	}
	if !strings.Contains(gotBody, "roleNames=admin") {
		t.Errorf("form body = %q, missing roleNames", gotBody)
	}
}

func TestCovB_GetRole(t *testing.T) {
	t.Run("success decodes JSON", func(t *testing.T) {
		var gotPath, gotQuery string
		srv := covBServer(func(w http.ResponseWriter, r *http.Request) {
			gotPath = r.URL.Path
			gotQuery = r.URL.RawQuery
			w.Header().Set("Content-Type", "application/json")
			_, _ = io.WriteString(w, `{"hudson.model.Hudson.Administer":true}`)
		})
		defer srv.Close()

		c := covBClient(t, srv)
		out := map[string]interface{}{}
		if err := c.GetRole(context.Background(), "globalRoles", "admin", &out); err != nil {
			t.Fatalf("GetRole error = %v", err)
		}
		if gotPath != "/role-strategy/strategy/getRole" {
			t.Errorf("path = %q", gotPath)
		}
		if !strings.Contains(gotQuery, "roleName=admin") || !strings.Contains(gotQuery, "type=globalRoles") {
			t.Errorf("query = %q", gotQuery)
		}
		if len(out) == 0 {
			t.Errorf("decoded map is empty")
		}
	})

	t.Run("non-200 errors", func(t *testing.T) {
		srv := covBServer(func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusForbidden)
		})
		defer srv.Close()

		c := covBClient(t, srv)
		out := map[string]interface{}{}
		if err := c.GetRole(context.Background(), "globalRoles", "admin", &out); err == nil {
			t.Fatal("expected an error for a 403 response")
		}
	})
}

// --- users ------------------------------------------------------------------

func TestCovB_CreateUser(t *testing.T) {
	var gotPath, gotBody string
	srv := covBServer(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		b, _ := io.ReadAll(r.Body)
		gotBody = string(b)
		w.WriteHeader(http.StatusOK)
		_, _ = io.WriteString(w, "{}")
	})
	defer srv.Close()

	c := covBClient(t, srv)
	if err := c.CreateUser(context.Background(), "alice", "s3cret", "Alice", "a@example.com"); err != nil {
		t.Fatalf("CreateUser error = %v", err)
	}
	if gotPath != "/securityRealm/createAccountByAdmin" {
		t.Errorf("path = %q", gotPath)
	}
	for _, want := range []string{"username=alice", "fullname=Alice", "email=a%40example.com"} {
		if !strings.Contains(gotBody, want) {
			t.Errorf("form body = %q, missing %q", gotBody, want)
		}
	}
}

func TestCovB_GetUser(t *testing.T) {
	t.Run("success decodes JSON", func(t *testing.T) {
		var gotPath string
		srv := covBServer(func(w http.ResponseWriter, r *http.Request) {
			gotPath = r.URL.Path
			w.Header().Set("Content-Type", "application/json")
			_, _ = io.WriteString(w, `{"id":"alice","fullName":"Alice"}`)
		})
		defer srv.Close()

		c := covBClient(t, srv)
		out := map[string]interface{}{}
		if err := c.GetUser(context.Background(), "alice", &out); err != nil {
			t.Fatalf("GetUser error = %v", err)
		}
		if gotPath != "/user/alice/api/json" {
			t.Errorf("path = %q", gotPath)
		}
		if out["id"] != "alice" {
			t.Errorf("decoded id = %v, want alice", out["id"])
		}
	})

	t.Run("404 surfaces not found", func(t *testing.T) {
		srv := covBServer(func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusNotFound)
		})
		defer srv.Close()

		c := covBClient(t, srv)
		out := map[string]interface{}{}
		err := c.GetUser(context.Background(), "ghost", &out)
		if err == nil || !strings.Contains(err.Error(), "not found") {
			t.Fatalf("error = %v, want not found", err)
		}
	})
}

func TestCovB_DeleteUser(t *testing.T) {
	t.Run("success posts to doDelete", func(t *testing.T) {
		var gotPath string
		srv := covBServer(func(w http.ResponseWriter, r *http.Request) {
			gotPath = r.URL.Path
			w.WriteHeader(http.StatusOK)
		})
		defer srv.Close()

		c := covBClient(t, srv)
		if err := c.DeleteUser(context.Background(), "alice"); err != nil {
			t.Fatalf("DeleteUser error = %v", err)
		}
		if gotPath != "/user/alice/doDelete" {
			t.Errorf("path = %q", gotPath)
		}
	})

	t.Run("non-200 errors", func(t *testing.T) {
		srv := covBServer(func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusInternalServerError)
		})
		defer srv.Close()

		c := covBClient(t, srv)
		if err := c.DeleteUser(context.Background(), "alice"); err == nil {
			t.Fatal("expected an error for a 500 response")
		}
	})
}

// --- plugins ----------------------------------------------------------------

func TestCovB_InstallPlugin(t *testing.T) {
	t.Run("success posts installNecessaryPlugins", func(t *testing.T) {
		var gotPath, gotBody string
		srv := covBServer(func(w http.ResponseWriter, r *http.Request) {
			gotPath = r.URL.Path
			b, _ := io.ReadAll(r.Body)
			gotBody = string(b)
			w.WriteHeader(http.StatusOK)
		})
		defer srv.Close()

		c := covBClient(t, srv)
		if err := c.InstallPlugin(context.Background(), "git", "1.2.3"); err != nil {
			t.Fatalf("InstallPlugin error = %v", err)
		}
		if gotPath != "/pluginManager/installNecessaryPlugins" {
			t.Errorf("path = %q", gotPath)
		}
		if !strings.Contains(gotBody, `plugin="git@1.2.3"`) {
			t.Errorf("body = %q, missing plugin spec", gotBody)
		}
	})

	t.Run("non-200 errors", func(t *testing.T) {
		srv := covBServer(func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusInternalServerError)
		})
		defer srv.Close()

		c := covBClient(t, srv)
		if err := c.InstallPlugin(context.Background(), "git", "1.2.3"); err == nil {
			t.Fatal("expected an error for a 500 response")
		}
	})
}

func TestCovB_UninstallPlugin(t *testing.T) {
	t.Run("success posts doUninstall", func(t *testing.T) {
		var gotPath string
		srv := covBServer(func(w http.ResponseWriter, r *http.Request) {
			gotPath = r.URL.Path
			w.WriteHeader(http.StatusOK)
		})
		defer srv.Close()

		c := covBClient(t, srv)
		if err := c.UninstallPlugin(context.Background(), "git"); err != nil {
			t.Fatalf("UninstallPlugin error = %v", err)
		}
		if gotPath != "/pluginManager/plugin/git/doUninstall" {
			t.Errorf("path = %q", gotPath)
		}
	})

	t.Run("non-200 errors", func(t *testing.T) {
		srv := covBServer(func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusInternalServerError)
		})
		defer srv.Close()

		c := covBClient(t, srv)
		if err := c.UninstallPlugin(context.Background(), "git"); err == nil {
			t.Fatal("expected an error for a 500 response")
		}
	})
}

func TestCovB_HasPlugin(t *testing.T) {
	t.Run("found", func(t *testing.T) {
		var gotPath string
		srv := covBServer(func(w http.ResponseWriter, r *http.Request) {
			gotPath = r.URL.Path
			w.Header().Set("Content-Type", "application/json")
			_, _ = io.WriteString(w, `{"plugins":[{"shortName":"git","longName":"Git plugin","version":"1.0","active":true,"enabled":true}]}`)
		})
		defer srv.Close()

		c := covBClient(t, srv)
		p, err := c.HasPlugin(context.Background(), "git")
		if err != nil {
			t.Fatalf("HasPlugin error = %v", err)
		}
		if p == nil || p.ShortName != "git" {
			t.Errorf("plugin = %+v, want shortName git", p)
		}
		if gotPath != "/pluginManager/api/json" {
			t.Errorf("path = %q", gotPath)
		}
	})

	t.Run("not installed returns nil", func(t *testing.T) {
		srv := covBServer(func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			_, _ = io.WriteString(w, `{"plugins":[]}`)
		})
		defer srv.Close()

		c := covBClient(t, srv)
		p, err := c.HasPlugin(context.Background(), "absent")
		if err != nil {
			t.Fatalf("HasPlugin error = %v", err)
		}
		if p != nil {
			t.Errorf("plugin = %+v, want nil", p)
		}
	})
}

func TestCovB_GetPlugin(t *testing.T) {
	t.Run("found", func(t *testing.T) {
		srv := covBServer(func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			_, _ = io.WriteString(w, `{"plugins":[{"shortName":"git","longName":"Git plugin","version":"1.0"}]}`)
		})
		defer srv.Close()

		c := covBClient(t, srv)
		p, err := c.GetPlugin(context.Background(), "git")
		if err != nil {
			t.Fatalf("GetPlugin error = %v", err)
		}
		if p == nil || p.ShortName != "git" {
			t.Errorf("plugin = %+v, want shortName git", p)
		}
	})

	t.Run("missing errors", func(t *testing.T) {
		srv := covBServer(func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			_, _ = io.WriteString(w, `{"plugins":[]}`)
		})
		defer srv.Close()

		c := covBClient(t, srv)
		_, err := c.GetPlugin(context.Background(), "absent")
		if err == nil || !strings.Contains(err.Error(), "not installed") {
			t.Fatalf("error = %v, want not installed", err)
		}
	})
}

// --- nodes ------------------------------------------------------------------

func TestCovB_GetNodeConfig(t *testing.T) {
	t.Run("success decodes config.xml", func(t *testing.T) {
		var gotPath string
		srv := covBServer(func(w http.ResponseWriter, r *http.Request) {
			gotPath = r.URL.Path
			w.Header().Set("Content-Type", "application/xml")
			_, _ = io.WriteString(w, `<?xml version='1.1' encoding='UTF-8'?>
<slave><name>agent1</name><numExecutors>2</numExecutors></slave>`)
		})
		defer srv.Close()

		c := covBClient(t, srv)
		var out struct {
			Name         string `xml:"name"`
			NumExecutors int    `xml:"numExecutors"`
		}
		if err := c.GetNodeConfig(context.Background(), "agent1", &out); err != nil {
			t.Fatalf("GetNodeConfig error = %v", err)
		}
		if gotPath != "/computer/agent1/config.xml" {
			t.Errorf("path = %q", gotPath)
		}
		if out.Name != "agent1" || out.NumExecutors != 2 {
			t.Errorf("decoded = %+v", out)
		}
	})

	t.Run("404 surfaces not found", func(t *testing.T) {
		srv := covBServer(func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusNotFound)
		})
		defer srv.Close()

		c := covBClient(t, srv)
		var out struct{}
		err := c.GetNodeConfig(context.Background(), "ghost", &out)
		if err == nil || !strings.Contains(err.Error(), "not found") {
			t.Fatalf("error = %v, want not found", err)
		}
	})
}

func TestCovB_GetAllNodes(t *testing.T) {
	var gotPath string
	srv := covBServer(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"computer":[{"displayName":"agent1"},{"displayName":"agent2"}]}`)
	})
	defer srv.Close()

	c := covBClient(t, srv)
	nodes, err := c.GetAllNodes(context.Background())
	if err != nil {
		t.Fatalf("GetAllNodes error = %v", err)
	}
	if gotPath != "/computer/api/json" {
		t.Errorf("path = %q", gotPath)
	}
	if len(nodes) != 2 {
		t.Errorf("nodes = %d, want 2", len(nodes))
	}
}

func TestCovB_GetAllJobNames(t *testing.T) {
	var gotPath string
	srv := covBServer(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"jobs":[{"name":"job1"}]}`)
	})
	defer srv.Close()

	c := covBClient(t, srv)
	jobs, err := c.GetAllJobNames(context.Background())
	if err != nil {
		t.Fatalf("GetAllJobNames error = %v", err)
	}
	if gotPath != "/api/json" {
		t.Errorf("path = %q", gotPath)
	}
	if len(jobs) != 1 || jobs[0].Name != "job1" {
		t.Errorf("jobs = %+v, want one named job1", jobs)
	}
}

func TestCovB_GetNode(t *testing.T) {
	var gotPath string
	srv := covBServer(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"displayName":"agent1"}`)
	})
	defer srv.Close()

	c := covBClient(t, srv)
	node, err := c.GetNode(context.Background(), "agent1")
	if err != nil {
		t.Fatalf("GetNode error = %v", err)
	}
	if gotPath != "/computer/agent1/api/json" {
		t.Errorf("path = %q", gotPath)
	}
	if node.GetName() != "agent1" {
		t.Errorf("node name = %q, want agent1", node.GetName())
	}
}

func TestCovB_CreateNode(t *testing.T) {
	srv := covBServer(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case strings.Contains(r.URL.Path, "doCreateItem"):
			w.WriteHeader(http.StatusOK)
		case strings.Contains(r.URL.Path, "api/json"):
			w.Header().Set("Content-Type", "application/json")
			_, _ = io.WriteString(w, `{"displayName":"agent1"}`)
		default:
			w.WriteHeader(http.StatusOK)
		}
	})
	defer srv.Close()

	c := covBClient(t, srv)
	node, err := c.CreateNode(context.Background(), "agent1", 1, "desc", "/home/jenkins", "linux")
	if err != nil {
		t.Fatalf("CreateNode error = %v", err)
	}
	if node == nil || node.GetName() != "agent1" {
		t.Errorf("node = %+v, want agent1", node)
	}
}

func TestCovB_DeleteNode(t *testing.T) {
	var gotPath string
	srv := covBServer(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		w.WriteHeader(http.StatusOK)
	})
	defer srv.Close()

	c := covBClient(t, srv)
	ok, err := c.DeleteNode(context.Background(), "agent1")
	if err != nil {
		t.Fatalf("DeleteNode error = %v", err)
	}
	if !ok {
		t.Error("DeleteNode returned false, want true")
	}
	if gotPath != "/computer/agent1/doDelete" {
		t.Errorf("path = %q", gotPath)
	}
}

// --- generic POST wrapper ---------------------------------------------------

func TestCovB_PostRequest(t *testing.T) {
	var gotMethod, gotPath, gotBody string
	srv := covBServer(func(w http.ResponseWriter, r *http.Request) {
		gotMethod, gotPath = r.Method, r.URL.Path
		b, _ := io.ReadAll(r.Body)
		gotBody = string(b)
		w.WriteHeader(http.StatusOK)
		_, _ = io.WriteString(w, "{}")
	})
	defer srv.Close()

	c := covBClient(t, srv)
	resp, err := c.PostRequest(context.Background(), "/some/endpoint/doThing", strings.NewReader("k=v"), &struct{}{}, map[string]string{})
	if err != nil {
		t.Fatalf("PostRequest error = %v", err)
	}
	if resp == nil || resp.StatusCode != http.StatusOK {
		t.Fatalf("resp = %+v, want 200", resp)
	}
	if gotMethod != http.MethodPost {
		t.Errorf("method = %q, want POST", gotMethod)
	}
	if gotPath != "/some/endpoint/doThing" {
		t.Errorf("path = %q", gotPath)
	}
	if gotBody != "k=v" {
		t.Errorf("body = %q, want k=v", gotBody)
	}
}
