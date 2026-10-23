package jenkins

import (
	"context"
	"encoding/base64"
	"net/url"
	"os"
	"strings"
	"testing"

	"github.com/hashicorp/terraform-plugin-framework/attr"
	"github.com/hashicorp/terraform-plugin-framework/diag"
	"github.com/hashicorp/terraform-plugin-framework/types"
)

func TestAccJenkinsConfigurationAsCode_secretReviewRegressions(t *testing.T) {
	if os.Getenv("TF_ACC") == "" {
		t.Skip("TF_ACC is not set")
	}
	testAccPreCheck(t)
	ctx := context.Background()
	r, s := cascTestResource("")
	r.client = testAccClient
	apply := func(document string, values map[string]string) diag.Diagnostics {
		m := cascNullModel("jenkins", document)
		m.SecretsWoVersion = types.StringValue("review")
		elems := map[string]attr.Value{}
		for k, v := range values {
			elems[k] = types.StringValue(v)
		}
		m.SecretsWo = types.MapValueMust(types.StringType, elems)
		var ds diag.Diagnostics
		r.apply(ctx, cascConfig(t, s, m), &m, &ds)
		return ds
	}
	read := func() *string {
		var got struct {
			Message *string `json:"message"`
		}
		form := url.Values{"script": {`println groovy.json.JsonOutput.toJson([message: jenkins.model.Jenkins.get().systemMessage])`}}.Encode()
		if _, err := testAccClient.Requester.Post(ctx, "/scriptText", strings.NewReader(form), &got, nil); err != nil {
			t.Fatal(err)
		}
		return got.Message
	}
	original := read()
	t.Cleanup(func() {
		if original == nil {
			if err := testAccClient.ApplyCASC(ctx, "jenkins:\n  systemMessage: null\n"); err != nil {
				t.Error(err)
			}
			return
		}
		if ds := apply("systemMessage: ${v}\n", map[string]string{"v": *original}); ds.HasError() {
			t.Error(ds)
		}
	})
	for _, tc := range []struct {
		name, document, want string
		values               map[string]string
	}{
		{"unclosed", "systemMessage: ${v}\n", "ab${cd", map[string]string{"v": "ab${cd"}},
		{"adjacent unclosed", "systemMessage: ${a}${b}\n", "a${b", map[string]string{"a": "a$", "b": "{b"}},
		{"suffix closes", "systemMessage: ${v}}\n", "ab${cd}", map[string]string{"v": "ab${cd"}},
		{"literal closed", "systemMessage: ${v}\n", "ab${cd}", map[string]string{"v": "ab${cd}"}},
		{"literal escaped", "systemMessage: ${v}\n", "ab^${cd}", map[string]string{"v": "ab^${cd}"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if ds := apply(tc.document, tc.values); ds.HasError() {
				t.Fatal(ds)
			}
			if got := read(); got == nil || *got != tc.want {
				t.Fatalf("controller did not preserve literal value: got %v, want %q", got, tc.want)
			}
		})
	}
	for _, tc := range []struct{ name, value, expression string }{
		{"escaped error", "tfacc-review-pw\"q\\line\nend", "${v}"},
		{"decoded error", base64.StdEncoding.EncodeToString([]byte("tfacc-review-decoded-secret")), "${decodeBase64:${v}}"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ds := apply("viewsTabBar: '"+tc.expression+"'\n", map[string]string{"v": tc.value})
			if !ds.HasError() {
				t.Fatal("expected JCasC rejection")
			}
			for _, d := range ds.Errors() {
				if strings.Contains(d.Detail(), "tfacc-review") || !strings.Contains(d.Detail(), "Controller error details are omitted") {
					t.Fatalf("unexpected diagnostic: %s", d.Detail())
				}
			}
		})
	}
}
