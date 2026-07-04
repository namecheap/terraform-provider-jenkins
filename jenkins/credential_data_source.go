package jenkins

import (
	"context"

	"github.com/hashicorp/terraform-plugin-framework/datasource"
	"github.com/hashicorp/terraform-plugin-framework/types"
)

// This file holds the shared read-only flow for the credential data sources
// (issue #98). The seven data sources differ only in their model, the gojenkins
// credential value they read, and which read-back fields they expose; the flow
// (folder scoping, domain defaulting, GetSingle, error template) is written once.

// credentialDSReadErrDetail is the read-error detail shared by the credential
// data sources, kept identical to the pre-refactor per-data-source string.
const credentialDSReadErrDetail = "An unexpected error occurred while parsing the data source read response. " +
	"Please report this issue to the provider developers.\n\n" +
	"Error: "

// credentialDataSourceReader captures the per-type parts of a credential data
// source Read: identity accessors, domain defaulting, and the read-back mapping.
type credentialDataSourceReader[M any] struct {
	folder      func(*M) types.String
	name        func(*M) types.String
	domain      func(*M) types.String
	setDomain   func(*M, string)
	setID       func(*M, string)
	newAPIValue func() interface{}
	fromAPI     func(api interface{}, m *M)
}

// credentialDataSource is the shared read-only implementation for the credential
// data sources. A concrete data source embeds *credentialDataSource[M] (gaining
// Read and Configure) and supplies Metadata/Schema itself.
type credentialDataSource[M any] struct {
	*dataSourceHelper
	reader credentialDataSourceReader[M]
}

func newCredentialDataSource[M any](reader credentialDataSourceReader[M]) *credentialDataSource[M] {
	return &credentialDataSource[M]{dataSourceHelper: newDataSourceHelper(), reader: reader}
}

func (d *credentialDataSource[M]) Read(ctx context.Context, req datasource.ReadRequest, resp *datasource.ReadResponse) {
	var m M
	resp.Diagnostics.Append(req.Config.Get(ctx, &m)...)
	if resp.Diagnostics.HasError() {
		return
	}

	cm := d.client.Credentials()
	folder := d.reader.folder(&m).ValueString()
	cm.Folder = formatFolderName(folder)

	if d.reader.domain(&m).IsNull() {
		d.reader.setDomain(&m, defaultCredentialDomain)
	}

	api := d.reader.newAPIValue()
	if err := cm.GetSingle(ctx, d.reader.domain(&m).ValueString(), d.reader.name(&m).ValueString(), api); err != nil {
		resp.Diagnostics.AddError("Unable to Read Data Source", credentialDSReadErrDetail+err.Error())
		return
	}

	d.reader.setID(&m, generateCredentialID(folder, d.reader.name(&m).ValueString()))
	d.reader.fromAPI(api, &m)
	resp.Diagnostics.Append(resp.State.Set(ctx, &m)...)
}
