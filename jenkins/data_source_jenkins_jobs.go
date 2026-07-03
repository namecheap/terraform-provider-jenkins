package jenkins

import (
	"context"
	"strings"

	jenkins "github.com/bndr/gojenkins"
	"github.com/hashicorp/terraform-plugin-framework/datasource"
	"github.com/hashicorp/terraform-plugin-framework/datasource/schema"
	"github.com/hashicorp/terraform-plugin-framework/types"
	"github.com/hashicorp/terraform-plugin-log/tflog"
)

// listInnerJobs returns the immediate children of the given folder (or the Jenkins
// root when folder is empty), as reported by Jenkins.
func listInnerJobs(ctx context.Context, client frameworkClient, folder string) ([]jenkins.InnerJob, error) {
	if len(extractFolders(folder)) == 0 {
		return client.GetAllJobNames(ctx)
	}
	name, parents := parseCanonicalJobID(folder)
	f, err := client.GetFolder(ctx, name, parents...)
	if err != nil {
		return nil, err
	}
	if f == nil || f.Raw == nil {
		return nil, nil
	}
	return f.Raw.Jobs, nil
}

// isFolderClass reports whether a job's `_class` denotes a container folder rather
// than a runnable job.
func isFolderClass(class string) bool {
	return strings.Contains(class, "folder.Folder")
}

type jobsDataSourceModel struct {
	ID     types.String `tfsdk:"id"`
	Folder types.String `tfsdk:"folder"`
	Jobs   types.Set    `tfsdk:"jobs"`
}

type jobsDataSource struct {
	*dataSourceHelper
}

var _ datasource.DataSourceWithConfigure = &jobsDataSource{}

func newJobsDataSource() datasource.DataSource {
	return &jobsDataSource{dataSourceHelper: newDataSourceHelper()}
}

func (d *jobsDataSource) Metadata(_ context.Context, req datasource.MetadataRequest, resp *datasource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_jobs"
}

func (d *jobsDataSource) Schema(_ context.Context, _ datasource.SchemaRequest, resp *datasource.SchemaResponse) {
	resp.Schema = schema.Schema{
		MarkdownDescription: "Lists the names of the jobs directly within a Jenkins folder (or the root).",
		Attributes: map[string]schema.Attribute{
			"id": schema.StringAttribute{
				Computed:            true,
				MarkdownDescription: "The folder whose jobs are listed, or `/` for the root.",
			},
			"folder": schema.StringAttribute{
				Optional:            true,
				MarkdownDescription: "The folder to list jobs from. If not set, the Jenkins root is listed.",
			},
			"jobs": schema.SetAttribute{
				Computed:            true,
				ElementType:         types.StringType,
				MarkdownDescription: "The names of the jobs (excluding folders) directly within the folder.",
			},
		},
	}
}

func (d *jobsDataSource) Read(ctx context.Context, req datasource.ReadRequest, resp *datasource.ReadResponse) {
	tflog.Debug(ctx, "jobsDataSource.Read")
	var data jobsDataSourceModel

	resp.Diagnostics.Append(req.Config.Get(ctx, &data)...)
	if resp.Diagnostics.HasError() {
		return
	}

	inner, err := listInnerJobs(ctx, d.client, data.Folder.ValueString())
	if err != nil {
		resp.Diagnostics.AddError("Unable to List Jobs", err.Error())
		return
	}

	names := make([]string, 0, len(inner))
	for _, j := range inner {
		if !isFolderClass(j.Class) {
			names = append(names, j.Name)
		}
	}

	jobs, diags := types.SetValueFrom(ctx, types.StringType, names)
	resp.Diagnostics.Append(diags...)
	if resp.Diagnostics.HasError() {
		return
	}
	data.Jobs = jobs
	if id := formatFolderID(extractFolders(data.Folder.ValueString())); id != "" {
		data.ID = types.StringValue(id)
	} else {
		data.ID = types.StringValue("/")
	}

	resp.Diagnostics.Append(resp.State.Set(ctx, &data)...)
}
