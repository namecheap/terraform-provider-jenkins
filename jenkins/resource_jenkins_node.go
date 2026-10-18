package jenkins

import (
	"context"

	"github.com/hashicorp/terraform-plugin-framework-validators/int64validator"
	"github.com/hashicorp/terraform-plugin-framework-validators/stringvalidator"
	"github.com/hashicorp/terraform-plugin-framework/diag"
	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/booldefault"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/int64default"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/planmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/stringdefault"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/stringplanmodifier"
	"github.com/hashicorp/terraform-plugin-framework/schema/validator"
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
	Mode         types.String `tfsdk:"mode"`
	WebSocket    types.Bool   `tfsdk:"web_socket"`

	JobRestriction *nodeJobRestrictionModel `tfsdk:"job_restriction"`
}

type nodeJobRestrictionModel struct {
	Regex          types.String `tfsdk:"regex"`
	CheckShortName types.Bool   `tfsdk:"check_short_name"`
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
Manages a permanent (static) agent node within Jenkins, launched as an inbound agent.

Changing ` + "`name`" + ` replaces the node. Every other attribute is updated in place through the
node's ` + "`config.xml`" + `, and a connected agent stays connected. Settings the resource does not
manage, such as other node properties or the agent's work directory options, keep the values
they have on the controller. Connect the agent out-of-band with the secret Jenkins issues for
it, which the ` + "`jenkins_node_secret`" + ` data source reads.`,
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
				MarkdownDescription: "The number of concurrent builds the node can run, at least `1`. Defaults to `1`.",
				// Jenkins accepts 0 but then drops the node's computer, and
				// with it /computer/<name>/: the node would leave state on the
				// next refresh while staying on the controller.
				Validators: []validator.Int64{
					int64validator.AtLeast(1),
				},
			},
			"description": schema.StringAttribute{
				Optional:            true,
				Computed:            true,
				Default:             stringdefault.StaticString(""),
				MarkdownDescription: "A human readable description of the node.",
			},
			"remote_fs": schema.StringAttribute{
				Required:            true,
				MarkdownDescription: "The absolute path to the agent's root/working directory on the agent machine (e.g. `/home/jenkins/agent`).",
			},
			"labels": schema.StringAttribute{
				Optional:            true,
				Computed:            true,
				Default:             stringdefault.StaticString(""),
				MarkdownDescription: "A space-separated list of labels used to target the node from job configurations.",
			},
			"mode": schema.StringAttribute{
				Optional:            true,
				Computed:            true,
				Default:             stringdefault.StaticString("NORMAL"),
				MarkdownDescription: "How Jenkins schedules builds on the node: `NORMAL` (the default) uses the node as much as possible, including for jobs with no label; `EXCLUSIVE` only builds jobs whose label expression matches the node.",
				Validators: []validator.String{
					stringvalidator.OneOf("NORMAL", "EXCLUSIVE"),
				},
			},
			"web_socket": schema.BoolAttribute{
				Optional:            true,
				Computed:            true,
				Default:             booldefault.StaticBool(false),
				MarkdownDescription: "The *Use WebSocket* option of the inbound launcher: the agent connects over WebSocket through the controller's HTTP(S) port instead of the TCP agent port. Jenkins hands it to agents that read their launch arguments from the controller; an agent started with `-webSocket` connects over WebSocket whatever this is set to. Defaults to `false`.",
			},
			"job_restriction": schema.SingleNestedAttribute{
				Optional:            true,
				MarkdownDescription: "Restricts the node to the jobs whose name matches a regular expression: the node refuses every other build, whatever labels it carries. Needs the Job Restrictions plugin (`job-restrictions`) on the controller. Removing the attribute removes the restriction. A restriction of another type set in the UI is not read, and is replaced when this attribute is set.",
				Attributes: map[string]schema.Attribute{
					"regex": schema.StringAttribute{
						Required:            true,
						MarkdownDescription: "Java regular expression the full name of the job (folders included) must match. The plugin accepts every job when the expression does not compile.",
						Validators: []validator.String{
							stringvalidator.LengthAtLeast(1),
						},
					},
					"check_short_name": schema.BoolAttribute{
						Optional:            true,
						Computed:            true,
						Default:             booldefault.StaticBool(false),
						MarkdownDescription: "The plugin's *Check short name* option. Defaults to `false`. Job Restrictions `242.v6edda_c9e4ca_f` stores it but matches the full name either way.",
					},
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
		map[string]string{"method": "JNLPLauncher", "mode": data.Mode.ValueString()},
	)
	if err != nil {
		resp.Diagnostics.AddError(
			"Unable to Create Resource",
			"An unexpected error occurred while creating the node.\n\nError: "+err.Error(),
		)
		return
	}

	data.ID = data.Name

	// doCreateItem sets every other attribute. The node properties and the
	// launcher's WebSocket flag need the node's config.xml.
	if data.JobRestriction != nil || data.WebSocket.ValueBool() {
		if diags := r.writeConfig(ctx, data); diags.HasError() {
			resp.Diagnostics.Append(diags...)
			// Deleting the half-configured node lets the next apply simply
			// create it again. Should the delete fail too, the node is
			// saved instead, tainted, so it is not orphaned on the controller.
			if _, err := r.client.DeleteNode(ctx, data.Name.ValueString()); err != nil {
				resp.Diagnostics.AddError(
					"Unable to Delete Partially Created Node",
					"Node "+data.Name.ValueString()+" was created but could not be configured, and deleting it failed. It is kept in state as tainted and will be replaced on the next apply.\n\nError: "+err.Error(),
				)
				resp.Diagnostics.Append(resp.State.Set(ctx, &data)...)
			}
			return
		}
	}

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
	data.Mode = types.StringValue(cfg.mode())
	data.WebSocket = types.BoolValue(cfg.webSocket())
	data.JobRestriction = nil
	if jr := cfg.jobRestriction(); jr != nil {
		data.JobRestriction = &nodeJobRestrictionModel{
			Regex:          types.StringValue(jr.Regex),
			CheckShortName: types.BoolValue(jr.CheckShortName),
		}
	}

	resp.Diagnostics.Append(resp.State.Set(ctx, &data)...)
}

func (r *nodeResource) Update(ctx context.Context, req resource.UpdateRequest, resp *resource.UpdateResponse) {
	tflog.Debug(ctx, "nodeResource.Update")
	var data nodeResourceModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &data)...)
	if resp.Diagnostics.HasError() {
		return
	}

	resp.Diagnostics.Append(r.writeConfig(ctx, data)...)
	if resp.Diagnostics.HasError() {
		return
	}

	data.ID = data.Name
	resp.Diagnostics.Append(resp.State.Set(ctx, &data)...)
}

// writeConfig applies the planned attributes to the node's config.xml with a
// read-modify-write, so what the resource does not manage keeps its value.
// Jenkins swaps the rebuilt node in place: a connected agent stays connected.
func (r *nodeResource) writeConfig(ctx context.Context, data nodeResourceModel) diag.Diagnostics {
	var diags diag.Diagnostics
	name := data.Name.ValueString()
	fail := func(action string, err error) diag.Diagnostics {
		diags.AddError(
			"Unable to Update Node Configuration",
			"An unexpected error occurred while "+action+" the configuration of node "+name+".\n\nError: "+err.Error(),
		)
		return diags
	}

	var cfg nodeConfig
	if err := r.client.GetNodeConfig(ctx, name, &cfg); err != nil {
		return fail("reading", err)
	}
	if err := cfg.apply(data); err != nil {
		return fail("preparing", err)
	}
	doc, err := cfg.render()
	if err != nil {
		return fail("preparing", err)
	}
	if err := r.client.UpdateNodeConfig(ctx, name, doc); err != nil {
		return fail("writing", err)
	}

	if data.JobRestriction == nil {
		return diags
	}
	// Jenkins drops a property whose class it cannot load and still answers
	// 200, so a missing plugin only shows in what it saved.
	var saved nodeConfig
	if err := r.client.GetNodeConfig(ctx, name, &saved); err != nil {
		return fail("reading back", err)
	}
	if saved.jobRestriction() == nil {
		diags.AddError(
			"Unable to Update Node Configuration",
			"Jenkins did not keep the job restriction of node "+name+". The Job Restrictions plugin (job-restrictions) must be installed on the controller.",
		)
	}
	return diags
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
