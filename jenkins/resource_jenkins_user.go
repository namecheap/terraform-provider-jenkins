package jenkins

import (
	"context"

	"github.com/hashicorp/terraform-plugin-framework/diag"
	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/planmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/stringplanmodifier"
	"github.com/hashicorp/terraform-plugin-framework/types"
	"github.com/hashicorp/terraform-plugin-log/tflog"
)

// jenkinsUserProperty is one entry of a user's property list; the Mailer plugin
// stores the email address here.
type jenkinsUserProperty struct {
	Address string `json:"address"`
}

// jenkinsUserResponse models the subset of GET /user/<id>/api/json this resource
// reads.
type jenkinsUserResponse struct {
	ID       string                `json:"id"`
	FullName string                `json:"fullName"`
	Property []jenkinsUserProperty `json:"property"`
}

type userResourceModel struct {
	ID       types.String `tfsdk:"id"`
	Username types.String `tfsdk:"username"`
	Password types.String `tfsdk:"password"`
	FullName types.String `tfsdk:"full_name"`
	Email    types.String `tfsdk:"email"`
}

type userResource struct {
	*resourceHelper
}

var _ resource.ResourceWithConfigure = &userResource{}
var _ resource.ResourceWithImportState = &userResource{}

func newUserResource() resource.Resource {
	return &userResource{
		resourceHelper: newResourceHelper(),
	}
}

func (r *userResource) Metadata(_ context.Context, req resource.MetadataRequest, resp *resource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_user"
}

func (r *userResource) Schema(_ context.Context, _ resource.SchemaRequest, resp *resource.SchemaResponse) {
	resp.Schema = schema.Schema{
		MarkdownDescription: `Manages a user account in Jenkins' own user database (the local security realm).

Requires the active security realm to be "Jenkins' own user database". On other realms (LDAP, OIDC, ...) operations fail with a clear diagnostic.

All attributes force replacement when changed: in-place updates of a local user's password, full name, or email are not supported, so a change recreates the account. API-token management is out of scope.`,
		Attributes: map[string]schema.Attribute{
			"id": schema.StringAttribute{
				Computed:            true,
				MarkdownDescription: "The user ID (same as `username`).",
				PlanModifiers: []planmodifier.String{
					stringplanmodifier.UseStateForUnknown(),
				},
			},
			"username": schema.StringAttribute{
				Required:            true,
				MarkdownDescription: "The unique login name of the user.",
				PlanModifiers: []planmodifier.String{
					stringplanmodifier.RequiresReplace(),
				},
			},
			"password": schema.StringAttribute{
				Required:            true,
				Sensitive:           true,
				MarkdownDescription: "The user's password. Stored in state (sensitive) and never read back from Jenkins. Changing it recreates the user.",
				PlanModifiers: []planmodifier.String{
					stringplanmodifier.RequiresReplace(),
				},
			},
			"full_name": schema.StringAttribute{
				Optional:            true,
				Computed:            true,
				MarkdownDescription: "The user's display name. Defaults to the username.",
				PlanModifiers: []planmodifier.String{
					stringplanmodifier.UseStateForUnknown(),
					stringplanmodifier.RequiresReplace(),
				},
			},
			"email": schema.StringAttribute{
				Optional:            true,
				MarkdownDescription: "The user's email address. Required when the Mailer plugin is installed (Jenkins rejects account creation without a valid address).",
				PlanModifiers: []planmodifier.String{
					stringplanmodifier.RequiresReplace(),
				},
			},
		},
	}
}

func (r *userResource) Create(ctx context.Context, req resource.CreateRequest, resp *resource.CreateResponse) {
	tflog.Debug(ctx, "userResource.Create")
	var data userResourceModel

	resp.Diagnostics.Append(req.Plan.Get(ctx, &data)...)
	if resp.Diagnostics.HasError() {
		return
	}

	if err := r.client.CreateUser(ctx, data.Username.ValueString(), data.Password.ValueString(), data.FullName.ValueString(), data.Email.ValueString()); err != nil {
		resp.Diagnostics.AddError("Unable to Create Resource", "An unexpected error occurred while creating the user.\n\nError: "+err.Error())
		return
	}

	if found := r.populate(ctx, &data, &resp.Diagnostics); !found && !resp.Diagnostics.HasError() {
		resp.Diagnostics.AddError(
			"Unable to Read Created Resource",
			"The user could not be read back after creation. Ensure the active security realm is Jenkins' own user database "+
				"(jenkins_user requires the local realm) and that a valid email is provided when the Mailer plugin is installed.",
		)
	}
	if resp.Diagnostics.HasError() {
		return
	}

	resp.Diagnostics.Append(resp.State.Set(ctx, &data)...)
}

func (r *userResource) Read(ctx context.Context, req resource.ReadRequest, resp *resource.ReadResponse) {
	tflog.Debug(ctx, "userResource.Read")
	var data userResourceModel

	resp.Diagnostics.Append(req.State.Get(ctx, &data)...)
	if resp.Diagnostics.HasError() {
		return
	}

	found := r.populate(ctx, &data, &resp.Diagnostics)
	if resp.Diagnostics.HasError() {
		return
	}
	if !found {
		resp.State.RemoveResource(ctx)
		return
	}

	resp.Diagnostics.Append(resp.State.Set(ctx, &data)...)
}

// Update is unreachable because every attribute forces replacement; it is
// implemented to satisfy the resource interface and, defensively, refreshes
// state from Jenkins.
func (r *userResource) Update(ctx context.Context, req resource.UpdateRequest, resp *resource.UpdateResponse) {
	tflog.Debug(ctx, "userResource.Update")
	var data userResourceModel

	resp.Diagnostics.Append(req.Plan.Get(ctx, &data)...)
	if resp.Diagnostics.HasError() {
		return
	}

	if found := r.populate(ctx, &data, &resp.Diagnostics); !found && !resp.Diagnostics.HasError() {
		resp.Diagnostics.AddError("Unable to Read Updated Resource", "the user could not be read back")
	}
	if resp.Diagnostics.HasError() {
		return
	}

	resp.Diagnostics.Append(resp.State.Set(ctx, &data)...)
}

func (r *userResource) Delete(ctx context.Context, req resource.DeleteRequest, resp *resource.DeleteResponse) {
	tflog.Debug(ctx, "userResource.Delete")
	var data userResourceModel

	resp.Diagnostics.Append(req.State.Get(ctx, &data)...)
	if resp.Diagnostics.HasError() {
		return
	}

	if err := r.client.DeleteUser(ctx, data.Username.ValueString()); err != nil {
		resp.Diagnostics.AddError("Unable to Delete Resource", err.Error())
	}
}

func (r *userResource) ImportState(ctx context.Context, req resource.ImportStateRequest, resp *resource.ImportStateResponse) {
	resp.Diagnostics.Append(resp.State.SetAttribute(ctx, path.Root("username"), req.ID)...)
	resp.Diagnostics.Append(resp.State.SetAttribute(ctx, path.Root("id"), req.ID)...)
}

// populate reads the user back from Jenkins and fills the readable fields
// (id, full_name, email). It leaves password untouched — Jenkins never returns
// it. Returns false (without an error diagnostic) when the user does not exist.
func (r *userResource) populate(ctx context.Context, data *userResourceModel, diags *diag.Diagnostics) bool {
	var u jenkinsUserResponse
	if err := r.client.GetUser(ctx, data.Username.ValueString(), &u); err != nil {
		if isNotFound(err) {
			return false
		}
		diags.AddError("Unable to Refresh Resource", "Could not read user "+data.Username.ValueString()+".\n\nError: "+err.Error())
		return false
	}
	if u.ID == "" {
		return false
	}

	data.ID = data.Username
	data.FullName = types.StringValue(u.FullName)

	email := ""
	for _, p := range u.Property {
		if p.Address != "" {
			email = p.Address
			break
		}
	}
	if email == "" {
		data.Email = types.StringNull()
	} else {
		data.Email = types.StringValue(email)
	}

	return true
}
