package jenkins

import (
	"context"

	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/int64default"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/int64planmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/planmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/stringdefault"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/stringplanmodifier"
	"github.com/hashicorp/terraform-plugin-framework/types"
	"github.com/hashicorp/terraform-plugin-log/tflog"
)

type nodeResourceModel struct {
	ID           types.String `tfsdk:"id"`
	Name         types.String `tfsdk:"name"`
	NumExecutors types.Int64  `tfsdk:"num_executors"`
	Description  types.String `tfsdk:"description"`
	RemoteFS     types.String `tfsdk:"remote_fs"`
	Labels       types.String `tfsdk:"labels"`
}

// nodeConfig maps the subset of a node's config.xml that this resource manages.
// It intentionally omits XMLName so it decodes regardless of the root element
// name (e.g. <slave>), which varies by node type.
type nodeConfig struct {
	Name         string `xml:"name"`
	Description  string `xml:"description"`
	RemoteFS     string `xml:"remoteFS"`
	NumExecutors int64  `xml:"numExecutors"`
	Label        string `xml:"label"`
}

type nodeResource struct {
	*resourceHelper
}

var _ resource.ResourceWithConfigure = &nodeResource{}
var _ resource.ResourceWithImportState = &nodeResource{}

func newNodeResource() resource.Resource {
	return &nodeResource{
		resourceHelper: newResourceHelper(),
	}
}

func (r *nodeResource) Metadata(_ context.Context, req resource.MetadataRequest, resp *resource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_node"
}

func (r *nodeResource) Schema(_ context.Context, _ resource.SchemaRequest, resp *resource.SchemaResponse) {
	resp.Schema = schema.Schema{
		MarkdownDescription: `
Manages a permanent (static) agent node within Jenkins, launched as an inbound (JNLP) agent.

Nodes are immutable: because Jenkins exposes no in-place update for a node's core
configuration, changing any attribute replaces the node. Use ` + "`num_executors`, `labels`" + ` and
the rest to describe the agent as code; connect the agent out-of-band with the inbound
(JNLP) secret Jenkins issues for it.`,
		Attributes: map[string]schema.Attribute{
			"id": schema.StringAttribute{
				Computed:            true,
				MarkdownDescription: "The name of the node, which is also its unique identifier.",
				PlanModifiers: []planmodifier.String{
					stringplanmodifier.UseStateForUnknown(),
				},
			},
			"name": schema.StringAttribute{
				Required:            true,
				MarkdownDescription: "The name of the node. Changing this forces a new node to be created.",
				PlanModifiers: []planmodifier.String{
					stringplanmodifier.RequiresReplace(),
				},
			},
			"num_executors": schema.Int64Attribute{
				Optional:            true,
				Computed:            true,
				Default:             int64default.StaticInt64(1),
				MarkdownDescription: "The number of concurrent builds the node can run. Defaults to `1`. Changing this forces a new node to be created.",
				PlanModifiers: []planmodifier.Int64{
					int64planmodifier.RequiresReplace(),
				},
			},
			"description": schema.StringAttribute{
				Optional:            true,
				Computed:            true,
				Default:             stringdefault.StaticString(""),
				MarkdownDescription: "A human readable description of the node. Changing this forces a new node to be created.",
				PlanModifiers: []planmodifier.String{
					stringplanmodifier.RequiresReplace(),
				},
			},
			"remote_fs": schema.StringAttribute{
				Required:            true,
				MarkdownDescription: "The absolute path to the agent's root/working directory on the agent machine (e.g. `/home/jenkins/agent`). Changing this forces a new node to be created.",
				PlanModifiers: []planmodifier.String{
					stringplanmodifier.RequiresReplace(),
				},
			},
			"labels": schema.StringAttribute{
				Optional:            true,
				Computed:            true,
				Default:             stringdefault.StaticString(""),
				MarkdownDescription: "A space-separated list of labels used to target the node from job configurations. Changing this forces a new node to be created.",
				PlanModifiers: []planmodifier.String{
					stringplanmodifier.RequiresReplace(),
				},
			},
		},
	}
}

func (r *nodeResource) Create(ctx context.Context, req resource.CreateRequest, resp *resource.CreateResponse) {
	tflog.Debug(ctx, "nodeResource.Create")
	var data nodeResourceModel

	resp.Diagnostics.Append(req.Plan.Get(ctx, &data)...)
	if resp.Diagnostics.HasError() {
		return
	}

	_, err := r.client.CreateNode(
		ctx,
		data.Name.ValueString(),
		int(data.NumExecutors.ValueInt64()),
		data.Description.ValueString(),
		data.RemoteFS.ValueString(),
		data.Labels.ValueString(),
	)
	if err != nil {
		resp.Diagnostics.AddError(
			"Unable to Create Resource",
			"An unexpected error occurred while creating the node.\n\nError: "+err.Error(),
		)
		return
	}

	data.ID = data.Name

	resp.Diagnostics.Append(resp.State.Set(ctx, &data)...)
}

func (r *nodeResource) Read(ctx context.Context, req resource.ReadRequest, resp *resource.ReadResponse) {
	tflog.Debug(ctx, "nodeResource.Read")
	var data nodeResourceModel

	resp.Diagnostics.Append(req.State.Get(ctx, &data)...)
	if resp.Diagnostics.HasError() {
		return
	}

	var cfg nodeConfig
	if err := r.client.GetNodeConfig(ctx, data.Name.ValueString(), &cfg); err != nil {
		if isNotFound(err) {
			resp.State.RemoveResource(ctx)
			return
		}
		resp.Diagnostics.AddError(
			"Unable to Refresh Resource",
			"An unexpected error occurred while reading the node configuration.\n\nError: "+err.Error(),
		)
		return
	}

	data.ID = data.Name
	data.NumExecutors = types.Int64Value(cfg.NumExecutors)
	data.Description = types.StringValue(cfg.Description)
	data.RemoteFS = types.StringValue(cfg.RemoteFS)
	data.Labels = types.StringValue(cfg.Label)

	resp.Diagnostics.Append(resp.State.Set(ctx, &data)...)
}

// Update should never run: every configurable attribute is RequiresReplace and
// Jenkins has no in-place node update. It persists the plan defensively.
func (r *nodeResource) Update(ctx context.Context, req resource.UpdateRequest, resp *resource.UpdateResponse) {
	tflog.Debug(ctx, "nodeResource.Update")
	var data nodeResourceModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &data)...)
	if resp.Diagnostics.HasError() {
		return
	}
	data.ID = data.Name
	resp.Diagnostics.Append(resp.State.Set(ctx, &data)...)
}

func (r *nodeResource) Delete(ctx context.Context, req resource.DeleteRequest, resp *resource.DeleteResponse) {
	tflog.Debug(ctx, "nodeResource.Delete")
	var data nodeResourceModel

	resp.Diagnostics.Append(req.State.Get(ctx, &data)...)
	if resp.Diagnostics.HasError() {
		return
	}

	if _, err := r.client.DeleteNode(ctx, data.Name.ValueString()); err != nil {
		resp.Diagnostics.AddError(
			"Unable to Delete Resource",
			"An unexpected error occurred while deleting the node.\n\nError: "+err.Error(),
		)
	}
}

func (r *nodeResource) ImportState(ctx context.Context, req resource.ImportStateRequest, resp *resource.ImportStateResponse) {
	resp.Diagnostics.Append(resp.State.SetAttribute(ctx, path.Root("name"), req.ID)...)
	resp.Diagnostics.Append(resp.State.SetAttribute(ctx, path.Root("id"), req.ID)...)
}
