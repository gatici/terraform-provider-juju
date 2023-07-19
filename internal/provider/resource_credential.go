package provider

import (
	"context"
	"fmt"
	"strconv"
	"strings"

	"github.com/hashicorp/terraform-plugin-framework/attr"
	"github.com/hashicorp/terraform-plugin-framework/diag"
	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/booldefault"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/objectplanmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/planmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/stringplanmodifier"
	"github.com/hashicorp/terraform-plugin-framework/types"
	"github.com/hashicorp/terraform-plugin-framework/types/basetypes"

	"github.com/hashicorp/terraform-plugin-log/tflog"
	"github.com/juju/terraform-provider-juju/internal/juju"
)

// Ensure provider defined types fully satisfy framework interfaces.
var _ resource.Resource = &credentialResource{}
var _ resource.ResourceWithConfigure = &credentialResource{}
var _ resource.ResourceWithImportState = &credentialResource{}

func NewCredentialResource() resource.Resource {
	return &credentialResource{}
}

type credentialResource struct {
	client *juju.Client
}

type credentialResourceModel struct {
	Cloud                types.Object `tfsdk:"cloud"`
	Attributes           types.Map    `tfsdk:"attributes"`
	AuthType             types.String `tfsdk:"auth_type"`
	ClientCredential     types.Bool   `tfsdk:"client_credential"`
	ControllerCredential types.Bool   `tfsdk:"controller_credential"`
	Name                 types.String `tfsdk:"name"`

	// ID required by the testing framework
	ID types.String `tfsdk:"id"`
}

func (c *credentialResource) Metadata(_ context.Context, req resource.MetadataRequest, resp *resource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_credential"
}

func (c *credentialResource) Schema(_ context.Context, _ resource.SchemaRequest, resp *resource.SchemaResponse) {
	resp.Schema = schema.Schema{
		Description: "A resource that represent a credential for a cloud.",
		Blocks: map[string]schema.Block{
			"cloud": schema.SingleNestedBlock{
				Description: "JuJu Cloud where the credentials will be used to access",
				Attributes: map[string]schema.Attribute{
					"name": schema.StringAttribute{
						Description: "The name of the cloud",
						Required:    true,
					},
				},
				PlanModifiers: []planmodifier.Object{
					objectplanmodifier.RequiresReplace(),
				},
			},
		},
		Attributes: map[string]schema.Attribute{
			"attributes": schema.MapAttribute{
				Description: "Credential attributes accordingly to the cloud",
				ElementType: types.StringType,
				Optional:    true,
			},
			"auth_type": schema.StringAttribute{
				Description: "Credential authorization type",
				Required:    true,
			},
			"client_credential": schema.BoolAttribute{
				Description: "Add credentials to the client",
				Optional:    true,
				Computed:    true,
				Default:     booldefault.StaticBool(false),
			},
			"controller_credential": schema.BoolAttribute{
				Description: "Add credentials to the controller",
				Optional:    true,
				Computed:    true,
				Default:     booldefault.StaticBool(true),
			},
			"name": schema.StringAttribute{
				Description: "The name to be assigned to the credential",
				Required:    true,
				PlanModifiers: []planmodifier.String{
					stringplanmodifier.RequiresReplace(),
				},
			},

			// ID required by the testing framework
			"id": schema.StringAttribute{
				Computed: true,
				PlanModifiers: []planmodifier.String{
					stringplanmodifier.UseStateForUnknown(),
				},
			},
		},
	}
}

func (c *credentialResource) Create(ctx context.Context, req resource.CreateRequest, resp *resource.CreateResponse) {
	var data *credentialResourceModel

	// Read Terraform configuration from the request into the resource model
	resp.Diagnostics.Append(req.Plan.Get(ctx, &data)...)
	if resp.Diagnostics.HasError() {
		return
	}

	// Access the fields
	// attributes
	attributes := convertRawAttributes(data.Attributes.Elements())

	// auth_type
	authType := data.AuthType.ValueString()

	// cloud.name
	cloudAttributes := data.Cloud.Attributes()
	cloudName := cloudAttributes["name"].(basetypes.StringValue).ValueString()

	// client_credential
	clientCredential := data.ClientCredential.ValueBool()

	// controller_credential
	controllerCredential := data.ControllerCredential.ValueBool()

	// name
	credentialName := data.Name.ValueString()

	// Prevent a segfault if client is not yet configured
	if c.client == nil {
		resp.Diagnostics.AddError(
			"Provider Error, Client Not Configured",
			"Unable to create credential resource. Expected configured Juju Client. "+
				"Please report this issue to the provider developers.",
		)
		return
	}

	// Perform logic or external calls
	response, err := c.client.Credentials.CreateCredential(juju.CreateCredentialInput{
		Attributes:           attributes,
		AuthType:             authType,
		ClientCredential:     clientCredential,
		CloudName:            cloudName,
		ControllerCredential: controllerCredential,
		Name:                 credentialName,
	})
	if err != nil {
		resp.Diagnostics.AddError("Client Error", fmt.Sprintf("Unable to create credential resource, got error: %s", err))
		return
	}
	tflog.Trace(ctx, fmt.Sprintf("created credential resource %q", credentialName))

	data.ID = types.StringValue(newIDFrom(credentialName, response.CloudName, clientCredential, controllerCredential))

	// Write the state data into the Response.State
	resp.Diagnostics.Append(resp.State.Set(ctx, &data)...)
}

func (c *credentialResource) Read(ctx context.Context, req resource.ReadRequest, resp *resource.ReadResponse) {
	var data *credentialResourceModel

	// Read Terraform configuration from the request into the resource model
	resp.Diagnostics.Append(req.State.Get(ctx, &data)...)
	if resp.Diagnostics.HasError() {
		return
	}

	// Access prior state data

	resID := retrieveValidateID(data, &resp.Diagnostics, "read")
	if resp.Diagnostics.HasError() {
		return
	}

	credentialName, cloudName, clientCredentialStr, controllerCredentialStr := resID[0], resID[1], resID[2], resID[3]

	// cloud
	cloudAttributes := map[string]attr.Value{
		"name": types.StringValue(cloudName),
	}
	attrTypes := map[string]attr.Type{
		"name": types.StringType,
	}
	cloud, errDiag := types.ObjectValue(attrTypes, cloudAttributes)
	resp.Diagnostics.Append(errDiag...)
	if resp.Diagnostics.HasError() {
		return
	}
	data.Cloud = cloud

	// client_credential & controller_credential
	clientCredential, controllerCredential, err := convertOptionsBool(clientCredentialStr, controllerCredentialStr)
	if err != nil {
		resp.Diagnostics.AddError("Client Error", fmt.Sprintf("Unable to read credential resource, got error: %s", err))
		return
	}
	data.ClientCredential = types.BoolValue(clientCredential)
	data.ControllerCredential = types.BoolValue(controllerCredential)

	// Prevent runtime to freak out if client is not configured
	if c.client == nil {
		resp.Diagnostics.AddError(
			"Provider Error, Client Not Configured",
			"Unable to read credential resource. Expected configured Juju Client. "+
				"Please report this issue to the provider developers.",
		)
		return
	}
	// Retrieve updated resource state from upstream
	response, err := c.client.Credentials.ReadCredential(juju.ReadCredentialInput{
		ClientCredential:     clientCredential,
		CloudName:            cloudName,
		ControllerCredential: controllerCredential,
		Name:                 credentialName,
	})
	if err != nil {
		resp.Diagnostics.AddError("Client Error", fmt.Sprintf("Unable to read credential resource, got error: %s", err))
		return
	}
	tflog.Trace(ctx, fmt.Sprintf("read credential resource %q", credentialName))

	// retrieve name & auth_type
	data.Name = types.StringValue(response.CloudCredential.Label)
	data.AuthType = types.StringValue(string(response.CloudCredential.AuthType()))

	// retrieve the attributes
	receivedAttributes := response.CloudCredential.Attributes()
	configuredAttributes := make(map[string]attr.Value)
	var attributesRaw map[string]attr.Value
	attributesRaw = data.Attributes.Elements()

	for k, rawAttr := range attributesRaw {
		configuredAttributes[k] = rawAttr
	}

	for configAtr := range configuredAttributes {
		if receivedValue, exists := receivedAttributes[configAtr]; exists {
			configuredAttributes[configAtr] = types.StringValue(attributeEntryToString(receivedValue))
		}
	}

	if len(configuredAttributes) != 0 {
		data.Attributes, errDiag = types.MapValueFrom(ctx, types.StringType, configuredAttributes)
		resp.Diagnostics.Append(errDiag...)
		if resp.Diagnostics.HasError() {
			return
		}
	}

	// Write the state data into the Response.State
	resp.Diagnostics.Append(resp.State.Set(ctx, &data)...)
}

func (c *credentialResource) Update(ctx context.Context, req resource.UpdateRequest, resp *resource.UpdateResponse) {
	var data, state *credentialResourceModel

	// Read current state of resource prior to the update into the 'state' model
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}

	// Read desired state of resource after the update into the 'data' model
	resp.Diagnostics.Append(req.Plan.Get(ctx, &data)...)
	if resp.Diagnostics.HasError() {
		return
	}

	// Return early if no change
	if data.AuthType.Equal(state.AuthType) &&
		data.ClientCredential.Equal(state.ClientCredential) &&
		data.ControllerCredential.Equal(state.ControllerCredential) &&
		data.Attributes.Equal(state.Attributes) {
		return
	}

	// Retrieve and validate the ID
	resID := retrieveValidateID(state, &resp.Diagnostics, "update")
	if resp.Diagnostics.HasError() {
		return
	}

	// Extract fields from the ID for the UpdateCredentialInput call
	// name & cloud.name fields
	credentialName, cloudName := resID[0], resID[1]

	// auth_type
	newAuthType := data.AuthType.ValueString()

	// client_credential & controller_credential
	newClientCredential := data.ClientCredential.ValueBool()
	newControllerCredential := data.ControllerCredential.ValueBool()

	// attributes
	newAttributes := convertRawAttributes(data.Attributes.Elements())

	// Prevent runtime to freak out if client is not configured
	if c.client == nil {
		resp.Diagnostics.AddError(
			"Provider Error, Client Not Configured",
			"Unable to update credential resource. Expected configured Juju Client. "+
				"Please report this issue to the provider developers.",
		)
		return
	}

	// Perform external call to modify resource
	err := c.client.Credentials.UpdateCredential(juju.UpdateCredentialInput{
		Attributes:           newAttributes,
		AuthType:             newAuthType,
		ClientCredential:     newClientCredential,
		CloudName:            cloudName,
		ControllerCredential: newControllerCredential,
		Name:                 credentialName,
	})
	if err != nil {
		resp.Diagnostics.AddError("Client Error", fmt.Sprintf("Unable to update credential resource, got error: %s", err))
		return
	}
	tflog.Trace(ctx, fmt.Sprintf("updated credential resource %q", credentialName))

	data.ID = types.StringValue(newIDFrom(credentialName, cloudName, newClientCredential, newControllerCredential))

	// Write the updated state data into the Response.State
	resp.Diagnostics.Append(resp.State.Set(ctx, &data)...)
}

func (c *credentialResource) Delete(ctx context.Context, req resource.DeleteRequest, resp *resource.DeleteResponse) {
	var data *credentialResourceModel

	// Read Terraform configuration from the request into the resource model
	resp.Diagnostics.Append(req.State.Get(ctx, &data)...)
	if resp.Diagnostics.HasError() {
		return
	}

	// Access prior state data

	resID := retrieveValidateID(data, &resp.Diagnostics, "update")
	if resp.Diagnostics.HasError() {
		return
	}
	// extract : name & cloud.name, client_credential, controller_credential
	credentialName, cloudName, clientCredentialStr, controllerCredentialStr := resID[0], resID[1], resID[2], resID[3]
	clientCredential, controllerCredential, err := convertOptionsBool(clientCredentialStr, controllerCredentialStr)
	if err != nil {
		resp.Diagnostics.AddError("Provider Error", err.Error())
		return
	}

	// Prevent runtime to freak out if client is not configured
	if c.client == nil {
		resp.Diagnostics.AddError(
			"Provider Error, Client Not Configured",
			"Unable to delete credential resource. Expected configured Juju Client. "+
				"Please report this issue to the provider developers.",
		)
		return
	}

	// Perform external call to destroy the resource
	err = c.client.Credentials.DestroyCredential(juju.DestroyCredentialInput{
		ClientCredential:     clientCredential,
		CloudName:            cloudName,
		ControllerCredential: controllerCredential,
		Name:                 credentialName,
	})
	if err != nil {
		resp.Diagnostics.AddError("Client Error", fmt.Sprintf("Unable to delete credential resource, got error: %s", err))
	}
	tflog.Trace(ctx, fmt.Sprintf("deleted credential resource %q", credentialName))
}

func (c *credentialResource) Configure(ctx context.Context, req resource.ConfigureRequest, resp *resource.ConfigureResponse) {
	// Prevent panic if the provider has not been configured.
	if req.ProviderData == nil {
		return
	}

	client, ok := req.ProviderData.(*juju.Client)
	if !ok {
		resp.Diagnostics.AddError(
			"Unexpected Resource Configure Type",
			fmt.Sprintf("Expected *juju.Client, got: %T. Please report this issue to the provider developers.", req.ProviderData),
		)
		return
	}
	c.client = client
}

func (c credentialResource) ImportState(ctx context.Context, req resource.ImportStateRequest, resp *resource.ImportStateResponse) {
	resource.ImportStatePassthroughID(ctx, path.Root("id"), req, resp)
}

func convertRawAttributes(attributesRaw map[string]attr.Value) map[string]string {
	newAttributes := make(map[string]string)
	for key, value := range attributesRaw {
		newAttributes[key] = attributeEntryToString(valueAttrToString(value))
	}
	return newAttributes
}

func newIDFrom(credentialName string, cloudName string, clientCredential bool, controllerCredential bool) string {
	return fmt.Sprintf("%s:%s:%t:%t", credentialName, cloudName, clientCredential, controllerCredential)
}

func retrieveValidateID(model *credentialResourceModel, diag *diag.Diagnostics, method string) []string {
	resID := strings.Split(model.ID.ValueString(), ":")
	if len(resID) != 4 {
		diag.AddError("Provider Error",
			fmt.Sprintf("unable to %v credential resource, invalid ID, expected {credentialName, cloudName, "+
				"isClient, isController} - given : %v",
				method, resID))
	}
	return resID
}

func valueAttrToString(input attr.Value) string {
	return types.StringValue(input.String()).ValueString()
}

func attributeEntryToString(input interface{}) string {
	switch t := input.(type) {
	case bool:
		return strconv.FormatBool(t)
	case int64:
		return strconv.FormatInt(t, 10)
	case float64:
		return strconv.FormatFloat(t, 'f', 0, 64)
	default:
		return input.(string)
	}
}

func convertOptionsBool(clientCredentialStr, controllerCredentialStr string) (bool, bool, error) {
	clientCredentialBool, err := strconv.ParseBool(clientCredentialStr)
	if err != nil {
		return false, false, fmt.Errorf("unable to parse client credential from provided ID")
	}

	controllerCredentialBool, err := strconv.ParseBool(controllerCredentialStr)
	if err != nil {
		return false, false, fmt.Errorf("unable to parse controller credential from provided ID")
	}

	return clientCredentialBool, controllerCredentialBool, nil
}
