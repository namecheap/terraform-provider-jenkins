package jenkins

import (
	"context"
	"fmt"
	"log"

	jenkins "github.com/bndr/gojenkins"
	"github.com/hashicorp/terraform-plugin-sdk/v2/diag"
	"github.com/hashicorp/terraform-plugin-sdk/v2/helper/schema"
)

func resourceJenkinsJob() *schema.Resource {
	return &schema.Resource{
		CreateContext: resourceJenkinsJobCreate,
		ReadContext:   resourceJenkinsJobRead,
		UpdateContext: resourceJenkinsJobUpdate,
		DeleteContext: resourceJenkinsJobDelete,
		Importer: &schema.ResourceImporter{
			StateContext: schema.ImportStatePassthroughContext,
		},
		Schema: map[string]*schema.Schema{
			"name": {
				Type:             schema.TypeString,
				Description:      "The unique name of the JenkinsCI job.",
				Required:         true,
				ForceNew:         true,
				ValidateDiagFunc: validateJobName,
			},
			"folder": {
				Type:             schema.TypeString,
				Description:      "The folder namespace that the job will be added to.",
				Optional:         true,
				ForceNew:         true,
				ValidateDiagFunc: validateFolderName,
			},
			"template": {
				Type:             schema.TypeString,
				Description:      "The configuration file template, used to communicate with Jenkins.",
				Required:         true,
				DiffSuppressFunc: templateDiff,
				ValidateDiagFunc: validateJobXML,
			},
			"disabled": {
				Type:        schema.TypeBool,
				Description: "Whether the job is disabled. When set, the provider enforces this state through the Jenkins enable/disable API and it takes precedence over any `<disabled>` element in the template; an out-of-band toggle is detected as drift. When omitted, the provider does not manage the job's enabled state and the template's own `<disabled>` value, if any, applies.",
				Optional:    true,
			},
		},
	}
}

// disabledConfig reports the user-configured value of the optional "disabled"
// attribute. set is false when the attribute is absent from configuration, in
// which case the provider must neither touch the job's enabled state nor report
// drift on it.
func disabledConfig(d *schema.ResourceData) (value bool, set bool) {
	raw := d.GetRawConfig()
	if raw.IsNull() || !raw.Type().HasAttribute("disabled") {
		return false, false
	}
	a := raw.GetAttr("disabled")
	if a.IsNull() || !a.IsKnown() {
		return false, false
	}
	return a.True(), true
}

// applyDisabledState forces job to the desired enabled/disabled state. Enable and
// Disable are idempotent (Jenkins returns 200 even when the job is already in the
// requested state), so the current state need not be read first.
func applyDisabledState(ctx context.Context, job *jenkins.Job, disabled bool) error {
	var err error
	if disabled {
		_, err = job.Disable(ctx)
	} else {
		_, err = job.Enable(ctx)
	}
	return err
}

func resourceJenkinsJobCreate(ctx context.Context, d *schema.ResourceData, meta interface{}) diag.Diagnostics {
	client := meta.(jenkinsClient)
	name := d.Get("name").(string)
	folderName := d.Get("folder").(string)

	// Validate that the folder exists
	if err := folderExists(ctx, client, folderName); err != nil {
		return diag.FromErr(fmt.Errorf("jenkins::create - Could not find folder '%s': %w", folderName, err))
	}

	xml := d.Get("template").(string)
	folders := extractFolders(folderName)
	job, err := client.CreateJobInFolder(ctx, xml, name, folders...)
	if err != nil {
		return diag.FromErr(fmt.Errorf("jenkins::create - Error creating job for %q in folder %s: %w", name, folderName, err))
	}

	log.Printf("[DEBUG] jenkins::create - job %q created in folder %s", name, folderName)
	d.SetId(formatFolderName(folderName + "/" + name))

	// Enforce the explicit disabled state after the template is applied, so it
	// wins over any <disabled> element the template itself carries.
	if want, set := disabledConfig(d); set {
		if err := applyDisabledState(ctx, job, want); err != nil {
			return diag.FromErr(fmt.Errorf("jenkins::create - Error setting disabled=%t on job %q: %w", want, name, err))
		}
	}

	return resourceJenkinsJobRead(ctx, d, meta)
}

func resourceJenkinsJobRead(ctx context.Context, d *schema.ResourceData, meta interface{}) diag.Diagnostics {
	client := meta.(jenkinsClient)
	name, folders := parseCanonicalJobID(d.Id())

	log.Printf("[DEBUG] jenkins::read - Looking for job %q", name)

	job, err := client.GetJob(ctx, name, folders...)
	if err != nil {
		if isNotFound(err) {
			// Job does not exist
			d.SetId("")
			return nil
		}

		return diag.FromErr(fmt.Errorf("jenkins::read - Job %q does not exist: %w", name, err))
	}

	config, err := job.GetConfig(ctx)
	if err != nil {
		return diag.FromErr(fmt.Errorf("jenkins::read - Job %q could not extract configuration: %v", job.Base, err))
	}

	log.Printf("[DEBUG] jenkins::read - Job %q exists", job.Base)
	d.SetId(job.Base)
	if err := d.Set("template", config); err != nil {
		return diag.FromErr(err)
	}

	if err := d.Set("name", name); err != nil {
		return diag.FromErr(err)
	}

	if err := d.Set("folder", formatFolderID(folders)); err != nil {
		return diag.FromErr(err)
	}

	// Only reflect the enabled state when the user manages it; otherwise leave
	// "disabled" unset so an out-of-band value never registers as drift.
	if _, set := disabledConfig(d); set {
		enabled, err := job.IsEnabled(ctx)
		if err != nil {
			return diag.FromErr(fmt.Errorf("jenkins::read - Job %q could not read enabled state: %w", name, err))
		}
		if err := d.Set("disabled", !enabled); err != nil {
			return diag.FromErr(err)
		}
	}

	return nil
}

func resourceJenkinsJobUpdate(ctx context.Context, d *schema.ResourceData, meta interface{}) diag.Diagnostics {
	client := meta.(jenkinsClient)
	name, folders := parseCanonicalJobID(d.Id())

	// grab job by current name
	job, err := client.GetJob(ctx, name, folders...)
	if err != nil {
		return diag.FromErr(fmt.Errorf("jenkins::update - Could not find job %q: %w", name, err))
	}

	xml := d.Get("template").(string)

	err = job.UpdateConfig(ctx, xml)
	if err != nil {
		return diag.FromErr(fmt.Errorf("jenkins::update - Error updating job %q configuration: %w", name, err))
	}

	// Apply the explicit disabled state after the template update, so it wins
	// over any <disabled> element the new template carries.
	if want, set := disabledConfig(d); set {
		if err := applyDisabledState(ctx, job, want); err != nil {
			return diag.FromErr(fmt.Errorf("jenkins::update - Error setting disabled=%t on job %q: %w", want, name, err))
		}
	}

	return resourceJenkinsJobRead(ctx, d, meta)
}

func resourceJenkinsJobDelete(ctx context.Context, d *schema.ResourceData, meta interface{}) diag.Diagnostics {
	client := meta.(jenkinsClient)
	name, folders := parseCanonicalJobID(d.Id())

	log.Printf("[DEBUG] jenkins::delete - Removing %q", name)

	ok, err := client.DeleteJobInFolder(ctx, name, folders...)
	if err != nil {
		return diag.FromErr(err)
	}

	log.Printf("[DEBUG] jenkins::delete - %q removed: %t", name, ok)
	return nil
}
