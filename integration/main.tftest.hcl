provider "docker" {}
provider "random" {}

run "setup" {
  module {
    source = "./setup"
  }

  providers = {
    docker = docker
    random = random
  }
}

run "jobs" {
  module {
    source = "./jobs"
  }

  variables {
    port = run.setup.port
  }

  providers = {
  }

  assert {
    condition     = chomp(data.jenkins_folder.example.template) == chomp(jenkins_folder.example.template)
    error_message = "${data.jenkins_folder.example.name} produced inconsistent XML"
  }
  assert {
    condition     = data.jenkins_folder.example.description == jenkins_folder.example.description
    error_message = "${data.jenkins_folder.example.name} did not match description"
  }
  assert {
    condition     = chomp(data.jenkins_folder.example_subfolder.template) == chomp(jenkins_folder.example_subfolder.template)
    error_message = "${data.jenkins_folder.example_subfolder.name} produced inconsistent XML"
  }
  assert {
    condition     = data.jenkins_folder.example_subfolder.description == jenkins_folder.example_subfolder.description
    error_message = "${data.jenkins_folder.example_subfolder.name} did not match description"
  }
  # The jenkins_job resource now preserves the user's template verbatim (semantic
  # equality), while the data source returns Jenkins' rendered config.xml, so the
  # two are semantically — not byte-for-byte — equal. Assert the data source read
  # back real configuration containing the expected description instead.
  assert {
    condition     = strcontains(data.jenkins_job.pipeline_scm.template, "An example pipeline job")
    error_message = "${data.jenkins_job.pipeline_scm.name} did not return the expected configuration"
  }
  assert {
    condition     = strcontains(data.jenkins_job.pipeline_inline.template, "Example inline script template")
    error_message = "${data.jenkins_job.pipeline_inline.name} did not return the expected configuration"
  }
  assert {
    condition     = strcontains(data.jenkins_job.freestyle.template, "An example freestyle job")
    error_message = "${data.jenkins_job.freestyle.name} did not return the expected configuration"
  }
  assert {
    condition     = data.jenkins_view.example.name == "example"
    error_message = "${data.jenkins_view.example.name} did not contain expected \"example\" value"
  }
}

run "credentials" {
  module {
    source = "./credentials"
  }

  variables {
    port = run.setup.port
  }

  providers = {
    random = random
  }

  assert {
    condition     = output.azure_service_principal.client_id == "123"
    error_message = "${nonsensitive(output.azure_service_principal.client_id)} did not contain expected \"123\" value"
  }
  assert {
    condition     = output.secret_file.filename == "secret-file.txt"
    error_message = "${nonsensitive(output.secret_file.filename)} did not contain expected \"secret-file.txt\" value"
  }
  assert {
    condition     = output.secret_text.secret == "barsoom"
    error_message = "${nonsensitive(output.secret_text.secret)} did not contain expected \"barsoom\" value"
  }
  assert {
    condition     = output.ssh.username == "example-username"
    error_message = "${nonsensitive(output.ssh.username)} did not contain expected \"example-username\" value"
  }
  assert {
    condition     = output.username.username == jenkins_credential_username.global.username
    error_message = "${output.username.username} data value did not match resource value"
  }
  assert {
    condition     = output.vault_approle.role_id == jenkins_credential_vault_approle.global.role_id
    error_message = "${output.vault_approle.role_id} data value did not match resource value"
  }
  assert {
    condition     = output.vault_approle.namespace == jenkins_credential_vault_approle.global.namespace
    error_message = "${output.vault_approle.namespace} data value did not match resource value"
  }
  assert {
    condition     = output.aws_cred.access_key == jenkins_credential_aws.global.access_key
    error_message = "${nonsensitive(output.aws_cred.access_key)} data value did not match resource value"
  }
  assert {
    condition     = output.aws_cred_folder.access_key == jenkins_credential_aws.folder_iam.access_key
    error_message = "${nonsensitive(output.aws_cred_folder.access_key)} data value did not match resource value"
  }
  assert {
    condition     = jenkins_credential_github_app.global.app_id == "12345"
    error_message = "${jenkins_credential_github_app.global.app_id} did not contain expected \"12345\" value"
  }
}
