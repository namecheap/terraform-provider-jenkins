package jenkins

import (
	"os"
	"testing"

	"github.com/hashicorp/terraform-plugin-framework/providerserver"
	"github.com/hashicorp/terraform-plugin-go/tfprotov6"
)

var (
	testAccProviders map[string]func() (tfprotov6.ProviderServer, error)
	testAccClient    *jenkinsAdapter
)

func init() {
	testAccProviders = map[string]func() (tfprotov6.ProviderServer, error){
		"jenkins": providerserver.NewProtocol6WithError(New()),
	}

	config := Config{
		ServerURL: os.Getenv("JENKINS_URL"),
		Username:  os.Getenv("JENKINS_USERNAME"),
		Password:  os.Getenv("JENKINS_PASSWORD"),
	}
	testAccClient, _ = newJenkinsClient(&config)
}

func testAccPreCheck(t *testing.T) {
	if v := os.Getenv("JENKINS_URL"); v == "" {
		t.Fatal("JENKINS_URL must be set for acceptance tests")
	}
	if v := os.Getenv("JENKINS_USERNAME"); v == "" {
		t.Fatal("JENKINS_USERNAME must be set for acceptance tests")
	}
	if v := os.Getenv("JENKINS_PASSWORD"); v == "" {
		t.Fatal("JENKINS_PASSWORD must be set for acceptance tests")
	}
}
