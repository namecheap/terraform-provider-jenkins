package jenkins

import (
	"context"

	"github.com/hashicorp/terraform-plugin-framework/datasource"
	"github.com/hashicorp/terraform-plugin-framework/datasource/schema"
	"github.com/hashicorp/terraform-plugin-framework/types"
)

type credentialCertificateDataSourceModel struct {
	ID          types.String `tfsdk:"id"`
	Name        types.String `tfsdk:"name"`
	Folder      types.String `tfsdk:"folder"`
	Description types.String `tfsdk:"description"`
	Domain      types.String `tfsdk:"domain"`
	Scope       types.String `tfsdk:"scope"`
}

type credentialCertificateDataSource struct {
	*credentialDataSource[credentialCertificateDataSourceModel]
}

var _ datasource.DataSourceWithConfigure = &credentialCertificateDataSource{}

func newCredentialCertificateDataSource() datasource.DataSource {
	return &credentialCertificateDataSource{
		credentialDataSource: newCredentialDataSource(certificateCredentialDataSourceReader()),
	}
}

func certificateCredentialDataSourceReader() credentialDataSourceReader[credentialCertificateDataSourceModel] {
	return credentialDataSourceReader[credentialCertificateDataSourceModel]{
		folder:      func(m *credentialCertificateDataSourceModel) types.String { return m.Folder },
		name:        func(m *credentialCertificateDataSourceModel) types.String { return m.Name },
		domain:      func(m *credentialCertificateDataSourceModel) types.String { return m.Domain },
		setDomain:   func(m *credentialCertificateDataSourceModel, v string) { m.Domain = types.StringValue(v) },
		setID:       func(m *credentialCertificateDataSourceModel, id string) { m.ID = types.StringValue(id) },
		newAPIValue: func() interface{} { return &CertificateCredentials{} },
		fromAPI: func(api interface{}, m *credentialCertificateDataSourceModel) {
			cred := api.(*CertificateCredentials)
			m.Scope = types.StringValue(cred.Scope)
			m.Description = types.StringValue(cred.Description)
		},
	}
}

func (d *credentialCertificateDataSource) Metadata(_ context.Context, req datasource.MetadataRequest, resp *datasource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_credential_certificate"
}

func (d *credentialCertificateDataSource) Schema(_ context.Context, _ datasource.SchemaRequest, resp *datasource.SchemaResponse) {
	resp.Schema = schema.Schema{
		MarkdownDescription: "Get the attributes of a certificate credential within Jenkins.",
		Attributes:          d.schemaCredential(map[string]schema.Attribute{}),
	}
}
